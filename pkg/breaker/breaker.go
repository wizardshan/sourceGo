package breaker

import (
	"math/rand"
	"sync"
	"time"
)

// 每秒一个桶, 记录该秒的请求成功、失败次数
type StatsBucket struct {
	success int
	fail    int
}

// 健康统计, 维护最近N秒的滑动窗口
type HealthStats struct {
	buckets    []StatsBucket // 滑动窗口, 每个桶1秒
	curTime    int64         // 当前窗口末尾的秒级unix时间戳， 桶内最大时间， 即最近的一个桶
	minStats   int           // 少于该打点数量直接返回健康,  配置信息
	healthRate float64       // 健康阀值， 配置信息
}

// 熔断器状态
type CircuitStatus int

const (
	CIRCUIT_NORMAL  CircuitStatus = 1 // 正常
	CIRCUIT_BREAK                 = 2 // 熔断
	CIRCUIT_RECOVER               = 3 // 恢复中
)

// 熔断器
type CircuitBreaker struct {
	mutex         sync.Mutex
	healthStats   *HealthStats  // 健康统计
	status        CircuitStatus // 熔断状态
	breakTime     int64         // 熔断的时间点(秒)
	breakPeriod   int           // 熔断封锁时间， 处于熔断的时间秒数，比如3秒， 属于配置信息 , 数据来自: breakPeriod
	recoverPeriod int           // 熔断恢复时间， 处于恢复阶段持续的秒数， 比如2秒，属于配置信息，数据来自: CircuitBreakerInfo
}

// 熔断配置
type CircuitBreakerInfo struct {
	BreakPeriod   int     // 熔断封锁时间
	RecoverPeriod int     // 熔断恢复时间
	WinSize       int     // 滑动窗口大小
	MinStats      int     // 最小统计样本
	HealthRate    float64 // 健康阀值
}

// 创建健康统计器
func createHealthStats(info *CircuitBreakerInfo) (healthStats *HealthStats) {
	healthStats = &HealthStats{
		minStats:   info.MinStats,
		healthRate: info.HealthRate,
	}
	healthStats.buckets = make([]StatsBucket, info.WinSize)
	healthStats.resetBuckets(healthStats.buckets[:])
	healthStats.curTime = CurUnixSecond()
	return
}

// 获取当前时间戳
func CurUnixSecond() int64 {
	return time.Now().Unix()
}

// 重置桶状态， 将桶内成功数， 失败数置零
func (healthStats *HealthStats) resetBuckets(buckets []StatsBucket) {
	for idx, _ := range buckets {
		buckets[idx].success = 0
		buckets[idx].fail = 0
	}
}

// 窗口滑动
func (healthStats *HealthStats) shiftBuckets() {
	now := CurUnixSecond()
	// 当前时间减去桶中最大值时间
	timeDiff := int(now - healthStats.curTime)
	if timeDiff <= 0 {
		return
	}

	// 如果时间差 大于桶的长度， 则需要重置桶内所有数据
	// 应用场景： 比如经过很长时间没有请求接口，突然来请求接口，这个时候滑动桶，则把所有的内容滑动没了
	if timeDiff >= len(healthStats.buckets) {
		healthStats.resetBuckets(healthStats.buckets[:])
	} else {
		// 如果当前时间大于 记录的最大时间， 并且小于桶的的个数, 则滑动桶
		healthStats.buckets = append(healthStats.buckets[:0], healthStats.buckets[timeDiff:]...)
		for i := 0; i < timeDiff; i++ {
			healthStats.buckets = append(healthStats.buckets, StatsBucket{})
		}
	}
	healthStats.curTime = now
}

// 成功打点
func (healthStats *HealthStats) success() {
	healthStats.shiftBuckets()
	healthStats.buckets[len(healthStats.buckets)-1].success++
}

// 失败打点
func (healthStats *HealthStats) fail() {
	healthStats.shiftBuckets()
	healthStats.buckets[len(healthStats.buckets)-1].fail++
}

// 判断是否健康
func (healthStats *HealthStats) isHealthy() (bool, float64) {
	healthStats.shiftBuckets()
	success := 0
	fail := 0
	for _, bucket := range healthStats.buckets {
		success += bucket.success
		fail += bucket.fail
	}
	total := success + fail
	// 没有样本
	if total == 0 {
		return true, 1
	}
	rate := (float64(success) / float64(total))
	// 样本不足
	if total < healthStats.minStats {
		return true, rate
	}
	// 样本充足
	return rate >= healthStats.healthRate, rate
}

// 创建熔断器
func CreateCircuitBreaker(info *CircuitBreakerInfo) (circuitBreaker *CircuitBreaker) {
	circuitBreaker = &CircuitBreaker{
		healthStats:   createHealthStats(info),
		status:        CIRCUIT_NORMAL,
		breakTime:     0,
		breakPeriod:   info.BreakPeriod,
		recoverPeriod: info.RecoverPeriod,
	}
	return
}

// 请求成功， 对外提供调用接口
func (circuitBreaker *CircuitBreaker) Success() {
	circuitBreaker.mutex.Lock()
	defer circuitBreaker.mutex.Unlock()
	circuitBreaker.healthStats.success()
}

// 请求失败， 对外提供调用接口
func (circuitBreaker *CircuitBreaker) Fail() {
	circuitBreaker.mutex.Lock()
	defer circuitBreaker.mutex.Unlock()
	circuitBreaker.healthStats.fail()
}

// 熔断器判定, 判读是否熔断
func (circuitBreaker *CircuitBreaker) IsBreak() (isBreak bool, isHealthy bool, healthRate float64) {
	// 最外层锁
	circuitBreaker.mutex.Lock()
	defer circuitBreaker.mutex.Unlock()
	now := CurUnixSecond()

	// 现在时间减去熔断开始时间， 作为熔断冷却期，
	breakLastTime := now - circuitBreaker.breakTime
	// 判断当前是否是健康状态
	isHealthy, healthRate = circuitBreaker.healthStats.isHealthy()
	isBreak = false
	// 状态机运转， 更改状态
	switch circuitBreaker.status {
	case CIRCUIT_NORMAL:
		// 非健康状态， 则轮转替换为[熔断中]
		if !isHealthy {
			circuitBreaker.status = CIRCUIT_BREAK
			circuitBreaker.breakTime = now
			isBreak = true
		}
	case CIRCUIT_BREAK:
		// 熔断中, 如果在熔断中， 或者不健康
		if breakLastTime < int64(circuitBreaker.breakPeriod) || !isHealthy {
			isBreak = true
		} else {
			// 否则切换到[恢复中]
			circuitBreaker.status = CIRCUIT_RECOVER
		}
	case CIRCUIT_RECOVER:
		// 在恢复中，如果是不健康状态， 则直接切换到[熔断中]
		if !isHealthy {
			circuitBreaker.status = CIRCUIT_BREAK
			circuitBreaker.breakTime = now
			isBreak = true
		} else {
			// 如果熔断的时间 已经大于 (熔断时间+恢复时间), 则切换到 正常状态
			if breakLastTime >= int64(circuitBreaker.breakPeriod+circuitBreaker.recoverPeriod) {
				circuitBreaker.status = CIRCUIT_NORMAL
			} else {
				// 随着时间间隔增加， 放行比例主键 需要逐渐增大
				passRate := float64(breakLastTime) / float64(circuitBreaker.breakPeriod+circuitBreaker.recoverPeriod)
				// 随机数随机生成0-1之间的数， 因为passRate逐渐增大， 所以break禁止的越来越少
				if rand.Float64() > passRate {
					isBreak = true
				}
			}
		}
	}
	return
}

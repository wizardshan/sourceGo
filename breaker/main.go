package main

import (
	"errors"
	"fmt"
	"sourceGo/pkg/gobreaker"
	"time"
)

// 请求无失败情况下 Interval默认为0s，不生成新的generation
func testInterval0() {

	var st gobreaker.Settings
	st.Name = "default"
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Second)
		result, err := cb.Execute(func() (int, error) {
			return i, nil
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

// 请求无失败情况下 Interval默认为10s，生成新的generation
func testInterval10() {

	var st gobreaker.Settings
	st.Name = "default"
	st.Interval = time.Duration(10) * time.Second
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Second)
		result, err := cb.Execute(func() (int, error) {
			return i, nil
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

// 请求成功失败交叉出现不进入熔断状态
func testZipper() {

	var st gobreaker.Settings
	st.Name = "default"
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Second)
		result, err := cb.Execute(func() (int, error) {
			if i%2 == 0 {
				return i, errors.New("zipper")
			}
			return i, nil
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

// 请求成功失败5次出现进入熔断状态，等待10s后，进入半开状态，失败1次，再进入熔断状态
func testConsecutiveFailure() {

	var st gobreaker.Settings
	st.Name = "default"
	st.Timeout = time.Duration(10) * time.Second
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Second)
		result, err := cb.Execute(func() (int, error) {
			return i, errors.New("zipper")
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

// 请求成功失败5次出现进入熔断状态，等待10s后，进入半开状态，成功1次进入关闭状态
func testConsecutiveFailureToSuccess1() {

	var st gobreaker.Settings
	st.Name = "default"
	st.Timeout = time.Duration(10) * time.Second
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Second)
		result, err := cb.Execute(func() (int, error) {
			if i < 15 {
				return i, errors.New("zipper")
			}
			return i, nil
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

// 请求成功失败5次出现进入熔断状态，等待10s后，进入半开状态，成功5次进入关闭状态
func testConsecutiveFailureToSuccess5() {

	var st gobreaker.Settings
	st.Name = "default"
	st.Timeout = time.Duration(10) * time.Second
	st.MaxRequests = 5
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Second)
		result, err := cb.Execute(func() (int, error) {
			if i < 15 {
				return i, errors.New("zipper")
			}
			return i, nil
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

// 请求成功失败率50%出现进入熔断状态，使用TotalFailures，需要配合设置Interval参数，默认Interval参数会出现Requests数字非常大，导致错误率不及时
func testPercentageFailure() {

	var st gobreaker.Settings
	st.Name = "default"
	st.Interval = time.Duration(1) * time.Second
	st.ReadyToTrip = func(counts gobreaker.Counts) bool {
		failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
		return counts.Requests >= 10 && failureRatio >= 0.5
	}
	st.Timeout = time.Duration(10) * time.Second
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		result, err := cb.Execute(func() (int, error) {
			if i%2 == 0 {
				return i, errors.New("zipper")
			}
			return i, nil
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

// 进入半开状态后续无请求会一直处于半开状态
func testHalfOpen() {

	var st gobreaker.Settings
	st.Name = "default"
	st.Timeout = time.Duration(10) * time.Second
	st.MaxRequests = 5
	var cb *gobreaker.CircuitBreaker[int]
	cb = gobreaker.NewCircuitBreaker[int](st)

	for i := 0; i < 17; i++ {
		if i < 15 {
			time.Sleep(1 * time.Second)
		} else {
			time.Sleep(20 * time.Second)
		}
		result, err := cb.Execute(func() (int, error) {
			if i < 15 {
				return i, errors.New("zipper")
			}
			return i, nil
		})
		fmt.Println(result, err, cb.State(), cb.Generation(), cb.Counts())
	}
}

func main() {
	testHalfOpen()
}

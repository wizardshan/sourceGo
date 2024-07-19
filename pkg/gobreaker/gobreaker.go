// Package gobreaker implements the Circuit Breaker pattern.
// See https://msdn.microsoft.com/en-us/library/dn589784.aspx.
package gobreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State is a type that represents a state of CircuitBreaker.
type State int

// These constants are states of CircuitBreaker.
const (
	// StateClosed 关闭状态：默认状态，该状态熔断器关闭，请求可以顺利到达下级服务，该状态下需要统计时间窗口内的成功次数和失败次数，如果达到故障率阈值，则进入打开状态
	StateClosed State = iota
	// StateHalfOpen 打开状态：该状态下熔断器打开，请求直接返回错误，如果有回调则直接调用回调方法。熔断器会在超时时间后进入半开状态，进行部分请求都尝试
	StateHalfOpen
	// StateOpen 半开状态：在该状态下，会通过部分请求进行尝试，如果尝试成功，则重置熔断器并进入关闭状态，否则进入打开状态
	StateOpen
)

var (
	// ErrTooManyRequests is returned when the CB state is half open and the requests count is over the cb maxRequests
	ErrTooManyRequests = errors.New("too many requests")
	// ErrOpenState is returned when the CB state is open
	ErrOpenState = errors.New("circuit breaker is open")
)

// String implements stringer interface.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return fmt.Sprintf("unknown state: %d", s)
	}
}

// Counts holds the numbers of requests and their successes/failures.
// CircuitBreaker clears the internal Counts either
// on the change of the state or at the closed-state intervals.
// Counts ignores the results of the requests sent before clearing.
type Counts struct {
	Requests             uint32 // 总请求数
	TotalSuccesses       uint32 // 总成功请求数
	TotalFailures        uint32 // 总失败请求数
	ConsecutiveSuccesses uint32 // 连续成功总数
	ConsecutiveFailures  uint32 // 连续失败总数
}

// 总请求数+1
func (c *Counts) onRequest() {
	c.Requests++
}

// 如果成功， 则成功计数加1，失败次数归零
func (c *Counts) onSuccess() {
	c.TotalSuccesses++
	c.ConsecutiveSuccesses++
	c.ConsecutiveFailures = 0
}

// 如果失败，则失败计数加1， 成功次数归零
func (c *Counts) onFailure() {
	c.TotalFailures++
	c.ConsecutiveFailures++
	c.ConsecutiveSuccesses = 0
}

// 成功，失败，总计清零
func (c *Counts) clear() {
	c.Requests = 0
	c.TotalSuccesses = 0
	c.TotalFailures = 0
	c.ConsecutiveSuccesses = 0
	c.ConsecutiveFailures = 0
}

// Settings configures CircuitBreaker:
//
// Name is the name of the CircuitBreaker.
//
// MaxRequests is the maximum number of requests allowed to pass through
// when the CircuitBreaker is half-open.
// If MaxRequests is 0, the CircuitBreaker allows only 1 request.
//
// Interval is the cyclic period of the closed state
// for the CircuitBreaker to clear the internal Counts.
// If Interval is less than or equal to 0, the CircuitBreaker doesn't clear internal Counts during the closed state.
//
// Timeout is the period of the open state,
// after which the state of the CircuitBreaker becomes half-open.
// If Timeout is less than or equal to 0, the timeout value of the CircuitBreaker is set to 60 seconds.
//
// ReadyToTrip is called with a copy of Counts whenever a request fails in the closed state.
// If ReadyToTrip returns true, the CircuitBreaker will be placed into the open state.
// If ReadyToTrip is nil, default ReadyToTrip is used.
// Default ReadyToTrip returns true when the number of consecutive failures is more than 5.
//
// OnStateChange is called whenever the state of the CircuitBreaker changes.
//
// IsSuccessful is called with the error returned from a request.
// If IsSuccessful returns true, the error is counted as a success.
// Otherwise the error is counted as a failure.
// If IsSuccessful is nil, default IsSuccessful is used, which returns false for all non-nil errors.
type Settings struct {
	Name          string                                  // 熔断器名称
	MaxRequests   uint32                                  // 半开状态 允许通过的最多请求数
	Interval      time.Duration                           // 关闭状态，多长时间内清空计数
	Timeout       time.Duration                           // 熔断状态， 冷却时间秒数， 超过此秒数，则自动进入到半开状态
	ReadyToTrip   func(counts Counts) bool                //  从关闭状态切换到熔断状态的条件判断
	OnStateChange func(name string, from State, to State) // 状态切换换时的回调函数
	IsSuccessful  func(err error) bool                    // 判断是否执行成功的业务函数
}

// CircuitBreaker is a state machine to prevent sending requests that are likely to fail.
type CircuitBreaker[T any] struct {
	name          string
	maxRequests   uint32
	interval      time.Duration
	timeout       time.Duration
	readyToTrip   func(counts Counts) bool
	isSuccessful  func(err error) bool
	onStateChange func(name string, from State, to State)

	mutex      sync.Mutex // 锁
	state      State      // 当前状态
	generation uint64     // 版本号
	counts     Counts     // 关联的计数器
	expiry     time.Time  // 有效期, 通过interval和timeout来驱动修改
}

// TwoStepCircuitBreaker is like CircuitBreaker but instead of surrounding a function
// with the breaker functionality, it only checks whether a request can proceed and
// expects the caller to report the outcome in a separate step using a callback.
type TwoStepCircuitBreaker[T any] struct {
	cb *CircuitBreaker[T]
}

// NewCircuitBreaker returns a new CircuitBreaker configured with the given Settings.
func NewCircuitBreaker[T any](st Settings) *CircuitBreaker[T] {
	cb := new(CircuitBreaker[T])

	cb.name = st.Name
	cb.onStateChange = st.OnStateChange

	if st.MaxRequests == 0 {
		cb.maxRequests = 1
	} else {
		cb.maxRequests = st.MaxRequests
	}

	if st.Interval <= 0 {
		cb.interval = defaultInterval
	} else {
		cb.interval = st.Interval
	}

	if st.Timeout <= 0 {
		cb.timeout = defaultTimeout
	} else {
		cb.timeout = st.Timeout
	}

	if st.ReadyToTrip == nil {
		cb.readyToTrip = defaultReadyToTrip
	} else {
		cb.readyToTrip = st.ReadyToTrip
	}

	if st.IsSuccessful == nil {
		cb.isSuccessful = defaultIsSuccessful
	} else {
		cb.isSuccessful = st.IsSuccessful
	}

	cb.toNewGeneration(time.Now())

	return cb
}

// NewTwoStepCircuitBreaker returns a new TwoStepCircuitBreaker configured with the given Settings.
func NewTwoStepCircuitBreaker[T any](st Settings) *TwoStepCircuitBreaker[T] {
	return &TwoStepCircuitBreaker[T]{
		cb: NewCircuitBreaker[T](st),
	}
}

const defaultInterval = time.Duration(0) * time.Second
const defaultTimeout = time.Duration(60) * time.Second

func defaultReadyToTrip(counts Counts) bool {
	return counts.ConsecutiveFailures > 5
}

func defaultIsSuccessful(err error) bool {
	return err == nil
}

// Generation returns the generation of the CircuitBreaker.
func (cb *CircuitBreaker[T]) Generation() uint64 {
	return cb.generation
}

func (cb *CircuitBreaker[T]) Name() string {
	return cb.name
}

// State returns the current state of the CircuitBreaker.
func (cb *CircuitBreaker[T]) State() State {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, _ := cb.currentState(now)
	return state
}

// Counts returns internal counters
func (cb *CircuitBreaker[T]) Counts() Counts {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return cb.counts
}

// Execute runs the given request if the CircuitBreaker accepts it.
// Execute returns an error instantly if the CircuitBreaker rejects the request.
// Otherwise, Execute returns the result of the request.
// If a panic occurs in the request, the CircuitBreaker handles it as an error
// and causes the same panic again.
func (cb *CircuitBreaker[T]) Execute(req func() (T, error)) (T, error) {
	generation, err := cb.beforeRequest()
	if err != nil {
		var defaultValue T
		return defaultValue, err
	}

	defer func() {
		e := recover()
		if e != nil {
			cb.afterRequest(generation, false)
			panic(e)
		}
	}()

	result, err := req()
	cb.afterRequest(generation, cb.isSuccessful(err))
	return result, err
}

// Name returns the name of the TwoStepCircuitBreaker.
func (tscb *TwoStepCircuitBreaker[T]) Name() string {
	return tscb.cb.Name()
}

// State returns the current state of the TwoStepCircuitBreaker.
func (tscb *TwoStepCircuitBreaker[T]) State() State {
	return tscb.cb.State()
}

// Counts returns internal counters
func (tscb *TwoStepCircuitBreaker[T]) Counts() Counts {
	return tscb.cb.Counts()
}

// Allow checks if a new request can proceed. It returns a callback that should be used to
// register the success or failure in a separate step. If the circuit breaker doesn't allow
// requests, it returns an error.
func (tscb *TwoStepCircuitBreaker[T]) Allow() (done func(success bool), err error) {
	generation, err := tscb.cb.beforeRequest()
	if err != nil {
		return nil, err
	}

	return func(success bool) {
		tscb.cb.afterRequest(generation, success)
	}, nil
}

func (cb *CircuitBreaker[T]) beforeRequest() (uint64, error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)

	if state == StateOpen {
		return generation, ErrOpenState
	} else if state == StateHalfOpen && cb.counts.Requests >= cb.maxRequests {
		return generation, ErrTooManyRequests
	}

	cb.counts.onRequest()
	return generation, nil
}

func (cb *CircuitBreaker[T]) afterRequest(before uint64, success bool) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	// 再次获取当前的状态， 如果当前请求周期太长，导致统计状态 已经变化，则需要将此次请求丢掉， 不加入到成功或者失败总数中
	state, generation := cb.currentState(now)
	if generation != before {
		return
	}

	if success {
		cb.onSuccess(state, now)
	} else {
		cb.onFailure(state, now)
	}
}

func (cb *CircuitBreaker[T]) onSuccess(state State, now time.Time) {
	switch state {
	case StateClosed:
		cb.counts.onSuccess()
	case StateHalfOpen:
		cb.counts.onSuccess()
		if cb.counts.ConsecutiveSuccesses >= cb.maxRequests {
			cb.setState(StateClosed, now)
		}
	}
}

func (cb *CircuitBreaker[T]) onFailure(state State, now time.Time) {
	switch state {
	case StateClosed:
		cb.counts.onFailure()
		if cb.readyToTrip(cb.counts) {
			cb.setState(StateOpen, now)
		}
	case StateHalfOpen:
		cb.setState(StateOpen, now)
	}
}

func (cb *CircuitBreaker[T]) currentState(now time.Time) (State, uint64) {
	switch cb.state {
	case StateClosed:
		if !cb.expiry.IsZero() && cb.expiry.Before(now) {
			cb.toNewGeneration(now)
		}
	case StateOpen:
		if cb.expiry.Before(now) {
			cb.setState(StateHalfOpen, now)
		}
	}
	return cb.state, cb.generation
}

func (cb *CircuitBreaker[T]) setState(state State, now time.Time) {
	if cb.state == state {
		return
	}

	prev := cb.state
	cb.state = state

	cb.toNewGeneration(now)

	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, prev, state)
	}
}

func (cb *CircuitBreaker[T]) toNewGeneration(now time.Time) {
	cb.generation++
	cb.counts.clear()

	var zero time.Time
	switch cb.state {
	case StateClosed:
		if cb.interval == 0 {
			cb.expiry = zero
		} else {
			cb.expiry = now.Add(cb.interval)
		}
	case StateOpen:
		cb.expiry = now.Add(cb.timeout)
	default: // StateHalfOpen
		cb.expiry = zero
	}
}

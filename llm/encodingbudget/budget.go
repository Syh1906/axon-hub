// Package encodingbudget limits optional request encoders on the gateway process.
package encodingbudget

const Capacity = 2

var slots = make(chan struct{}, Capacity)

func TryAcquire() bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func Release() { <-slots }

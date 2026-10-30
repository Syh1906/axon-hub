// Package budget contains the shared, storage-independent spending rules.
package budget

import (
	"fmt"
	"math"

	"github.com/shopspring/decimal"
)

// Amount stores one hundred millionth of the accounting currency.
type Amount int64

// ParseAmount rejects negative, overflowing and overprecise administrator input.
func ParseAmount(value string) (Amount, error) {
	d, err := decimal.NewFromString(value)
	if err != nil {
		return 0, fmt.Errorf("invalid budget amount: %w", err)
	}
	return amountFromDecimal(d, false)
}

// ReserveAmount rounds estimates upwards so rounding never allocates extra money.
func ReserveAmount(value decimal.Decimal) (Amount, error) {
	return amountFromDecimal(value, true)
}

func amountFromDecimal(value decimal.Decimal, roundUp bool) (Amount, error) {
	if value.IsNegative() {
		return 0, fmt.Errorf("budget amount must not be negative")
	}
	scaled := value.Shift(8)
	if roundUp {
		scaled = scaled.Ceil()
	} else if !scaled.Equal(scaled.Truncate(0)) {
		return 0, fmt.Errorf("budget amount supports at most eight decimal places")
	}
	if scaled.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, fmt.Errorf("budget amount exceeds storage range")
	}
	return Amount(scaled.IntPart()), nil
}

func (a Amount) Decimal() decimal.Decimal { return decimal.NewFromInt(int64(a)).Shift(-8) }

func (a Amount) String() string { return a.Decimal().StringFixed(8) }

// Fits uses subtraction to avoid overflow when limits are near the storage bound.
// A zero limit means unlimited; settled and reserved amounts remain accounted for.
func Fits(limit, settled, reserved, estimate Amount) bool {
	if limit < 0 || settled < 0 || reserved < 0 || estimate < 0 {
		return false
	}
	if limit == 0 {
		return true
	}
	if settled >= limit || reserved >= limit-settled {
		return false
	}
	return estimate <= limit-settled-reserved
}

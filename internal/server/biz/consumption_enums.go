package biz

import (
	"fmt"
	"io"
	"slices"
	"strconv"
)

func consumptionEnum(value any, allowed ...string) (string, error) {
	text, ok := value.(string)
	if !ok || !slices.Contains(allowed, text) {
		return "", fmt.Errorf("invalid consumption enum value %v", value)
	}
	return text, nil
}

func (v ConsumptionDimension) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(v)))
}
func (v *ConsumptionDimension) UnmarshalGQL(value any) error {
	text, err := consumptionEnum(value, "USER", "API_KEY", "API_KEY_GROUP", "CHANNEL")
	if err == nil {
		*v = ConsumptionDimension(text)
	}
	return err
}
func (v ConsumptionMetric) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(v)))
}
func (v *ConsumptionMetric) UnmarshalGQL(value any) error {
	text, err := consumptionEnum(value, "COST", "CACHE")
	if err == nil {
		*v = ConsumptionMetric(text)
	}
	return err
}
func (v ConsumptionPeriod) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(v)))
}
func (v *ConsumptionPeriod) UnmarshalGQL(value any) error {
	text, err := consumptionEnum(value, "TODAY", "YESTERDAY", "WEEK", "MONTH", "ALL", "CUSTOM")
	if err == nil {
		*v = ConsumptionPeriod(text)
	}
	return err
}

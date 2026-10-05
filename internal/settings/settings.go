// Package settings defines the owner-scoped controls shared by learning algorithms.
package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

type Values struct {
	LearningMode                   string    `json:"learningMode"`
	FocusBatchSize                 int       `json:"focusBatchSize"`
	FastAnswerSeconds              int       `json:"fastAnswerSeconds"`
	RequestedRetention             float64   `json:"requestedRetention"`
	MaximumIntervalDays            int       `json:"maximumIntervalDays"`
	LearningStepsMinutes           []float64 `json:"learningStepsMinutes"`
	RelearningStepsMinutes         []float64 `json:"relearningStepsMinutes"`
	MasteryDays                    int       `json:"masteryDays"`
	MasteryIntervalDays            int       `json:"masteryIntervalDays"`
	PresentationCooldownMinutes    int       `json:"presentationCooldownMinutes"`
	RecentPresentationCount        int       `json:"recentPresentationCount"`
	NewShareMin                    float64   `json:"newShareMin"`
	NewShareMax                    float64   `json:"newShareMax"`
	LearnedShareMin                float64   `json:"learnedShareMin"`
	LearnedShareMax                float64   `json:"learnedShareMax"`
	LearnedWeightDivisor           float64   `json:"learnedWeightDivisor"`
	LowUsefulnessWeight            float64   `json:"lowUsefulnessWeight"`
	HighUsefulnessWeight           float64   `json:"highUsefulnessWeight"`
	LowInterestWeight              float64   `json:"lowInterestWeight"`
	HighInterestWeight             float64   `json:"highInterestWeight"`
	UnseenExposureWeight           float64   `json:"unseenExposureWeight"`
	ExposureRecoveryHours          float64   `json:"exposureRecoveryHours"`
	ReinforcementCooldownHours     float64   `json:"reinforcementCooldownHours"`
	ReinforcementMaxWordShare      float64   `json:"reinforcementMaxWordShare"`
	TroublesomeConsecutiveFailures int       `json:"troublesomeConsecutiveFailures"`
	TroublesomeLapses              int       `json:"troublesomeLapses"`
}

// UnmarshalJSON accepts a complete settings snapshot, not a partial patch. Missing
// and null scalars must not silently disable policies whose valid range includes 0.
func (values *Values) UnmarshalJSON(data []byte) error {
	type plainValues Values
	decoded := plainValues{
		FastAnswerSeconds: -1, PresentationCooldownMinutes: -1, RecentPresentationCount: -1,
		LearnedShareMin: -1, LearnedShareMax: -1, ReinforcementCooldownHours: -1,
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if decoded.LearningStepsMinutes == nil || decoded.RelearningStepsMinutes == nil {
		return fmt.Errorf("learningStepsMinutes and relearningStepsMinutes must be arrays (empty arrays disable steps)")
	}
	result := Values(decoded)
	if err := result.Validate(); err != nil {
		return err
	}
	*values = result
	return nil
}

// Defaults returns independent step slices so a draft cannot mutate other owners.
func Defaults() Values {
	return Values{
		LearningMode: "mixed", FocusBatchSize: 10,
		FastAnswerSeconds: 30, RequestedRetention: .9, MaximumIntervalDays: 36500,
		LearningStepsMinutes: []float64{1, 10}, RelearningStepsMinutes: []float64{10},
		MasteryDays: 5, MasteryIntervalDays: 21,
		PresentationCooldownMinutes: 30, RecentPresentationCount: 3,
		NewShareMin: .2, NewShareMax: .8, LearnedShareMin: .1, LearnedShareMax: .4,
		LearnedWeightDivisor: 9, LowUsefulnessWeight: .5, HighUsefulnessWeight: 2,
		LowInterestWeight: .5, HighInterestWeight: 2, UnseenExposureWeight: 4,
		ExposureRecoveryHours: 24, ReinforcementCooldownHours: 6, ReinforcementMaxWordShare: .25,
		TroublesomeConsecutiveFailures: 2, TroublesomeLapses: 3,
	}
}

func (values Values) Validate() error {
	if values.LearningMode != "mixed" && values.LearningMode != "focused" {
		return fmt.Errorf("learningMode must be mixed or focused")
	}
	for _, field := range []struct {
		name             string
		value, low, high float64
	}{
		{"focusBatchSize", float64(values.FocusBatchSize), 1, 100},
		{"fastAnswerSeconds", float64(values.FastAnswerSeconds), 0, 300},
		{"requestedRetention", values.RequestedRetention, .7, .99},
		{"maximumIntervalDays", float64(values.MaximumIntervalDays), 1, 36500},
		{"masteryDays", float64(values.MasteryDays), 1, 365},
		{"masteryIntervalDays", float64(values.MasteryIntervalDays), 1, float64(values.MaximumIntervalDays)},
		{"presentationCooldownMinutes", float64(values.PresentationCooldownMinutes), 0, 1440},
		{"recentPresentationCount", float64(values.RecentPresentationCount), 0, 100},
		{"newShareMin", values.NewShareMin, .01, .99}, {"newShareMax", values.NewShareMax, values.NewShareMin, .99},
		{"learnedShareMin", values.LearnedShareMin, 0, .99}, {"learnedShareMax", values.LearnedShareMax, values.LearnedShareMin, .99},
		{"learnedWeightDivisor", values.LearnedWeightDivisor, 1, 100},
		{"lowUsefulnessWeight", values.LowUsefulnessWeight, .05, 20}, {"highUsefulnessWeight", values.HighUsefulnessWeight, .05, 20},
		{"lowInterestWeight", values.LowInterestWeight, .05, 20}, {"highInterestWeight", values.HighInterestWeight, .05, 20},
		{"unseenExposureWeight", values.UnseenExposureWeight, .05, 20},
		{"exposureRecoveryHours", values.ExposureRecoveryHours, 1, 168},
		{"reinforcementCooldownHours", values.ReinforcementCooldownHours, 0, 720},
		{"reinforcementMaxWordShare", values.ReinforcementMaxWordShare, .05, 1},
		{"troublesomeConsecutiveFailures", float64(values.TroublesomeConsecutiveFailures), 1, 100},
		{"troublesomeLapses", float64(values.TroublesomeLapses), 1, 100},
	} {
		if math.IsNaN(field.value) || math.IsInf(field.value, 0) || field.value < field.low || field.value > field.high {
			return fmt.Errorf("%s must be between %g and %g", field.name, field.low, field.high)
		}
	}
	for _, field := range []struct {
		name  string
		steps []float64
	}{
		{"learningStepsMinutes", values.LearningStepsMinutes}, {"relearningStepsMinutes", values.RelearningStepsMinutes},
	} {
		if len(field.steps) > 10 {
			return fmt.Errorf("%s must contain at most 10 steps", field.name)
		}
		previous := 0.0
		for _, step := range field.steps {
			if math.IsNaN(step) || math.IsInf(step, 0) || step <= previous || step >= 1440 {
				return fmt.Errorf("%s must be strictly increasing positive minutes below 1440", field.name)
			}
			previous = step
		}
	}
	return nil
}

package settings

import (
	"encoding/json"
	"math"
	"testing"
)

func TestValidateRejectsInvalidSettings(t *testing.T) {
	for name, change := range map[string]func(*Values){
		"unknown learning mode":           func(v *Values) { v.LearningMode = "random" },
		"empty learning mode":             func(v *Values) { v.LearningMode = "" },
		"zero batch size":                 func(v *Values) { v.FocusBatchSize = 0 },
		"large batch size":                func(v *Values) { v.FocusBatchSize = 101 },
		"negative fast threshold":         func(v *Values) { v.FastAnswerSeconds = -1 },
		"long fast threshold":             func(v *Values) { v.FastAnswerSeconds = 301 },
		"retention below range":           func(v *Values) { v.RequestedRetention = .69 },
		"retention above range":           func(v *Values) { v.RequestedRetention = 1 },
		"nonfinite retention":             func(v *Values) { v.RequestedRetention = math.NaN() },
		"zero interval":                   func(v *Values) { v.MaximumIntervalDays = 0 },
		"excessive interval":              func(v *Values) { v.MaximumIntervalDays = 36501 },
		"mastery exceeds interval":        func(v *Values) { v.MaximumIntervalDays = 20 },
		"zero mastery":                    func(v *Values) { v.MasteryDays = 0 },
		"excessive mastery":               func(v *Values) { v.MasteryDays = 366 },
		"zero mastery interval":           func(v *Values) { v.MasteryIntervalDays = 0 },
		"negative cooldown":               func(v *Values) { v.PresentationCooldownMinutes = -1 },
		"excessive cooldown":              func(v *Values) { v.PresentationCooldownMinutes = 1441 },
		"negative recent count":           func(v *Values) { v.RecentPresentationCount = -1 },
		"excessive recent count":          func(v *Values) { v.RecentPresentationCount = 101 },
		"inverted new shares":             func(v *Values) { v.NewShareMin = .9 },
		"new share starvation":            func(v *Values) { v.NewShareMin = 0 },
		"new share overflow":              func(v *Values) { v.NewShareMax = 1 },
		"inverted learned shares":         func(v *Values) { v.LearnedShareMax = 0 },
		"negative learned share":          func(v *Values) { v.LearnedShareMin = -.1 },
		"learned share overflow":          func(v *Values) { v.LearnedShareMax = 1 },
		"zero divisor":                    func(v *Values) { v.LearnedWeightDivisor = 0 },
		"excessive divisor":               func(v *Values) { v.LearnedWeightDivisor = 101 },
		"zero usefulness":                 func(v *Values) { v.LowUsefulnessWeight = 0 },
		"excessive usefulness":            func(v *Values) { v.HighUsefulnessWeight = 21 },
		"negative interest":               func(v *Values) { v.LowInterestWeight = -1 },
		"nonfinite interest":              func(v *Values) { v.HighInterestWeight = math.Inf(1) },
		"zero unseen weight":              func(v *Values) { v.UnseenExposureWeight = 0 },
		"zero recovery":                   func(v *Values) { v.ExposureRecoveryHours = 0 },
		"long recovery":                   func(v *Values) { v.ExposureRecoveryHours = 169 },
		"negative reinforcement cooldown": func(v *Values) { v.ReinforcementCooldownHours = -1 },
		"long reinforcement cooldown":     func(v *Values) { v.ReinforcementCooldownHours = 721 },
		"small reinforcement cap":         func(v *Values) { v.ReinforcementMaxWordShare = .049 },
		"large reinforcement cap":         func(v *Values) { v.ReinforcementMaxWordShare = 1.01 },
		"zero failures":                   func(v *Values) { v.TroublesomeConsecutiveFailures = 0 },
		"excessive lapses":                func(v *Values) { v.TroublesomeLapses = 101 },
		"zero learning step":              func(v *Values) { v.LearningStepsMinutes = []float64{0} },
		"duplicate learning steps":        func(v *Values) { v.LearningStepsMinutes = []float64{1, 1} },
		"descending relearning steps":     func(v *Values) { v.RelearningStepsMinutes = []float64{10, 1} },
		"day long step":                   func(v *Values) { v.LearningStepsMinutes = []float64{1440} },
		"nonfinite step":                  func(v *Values) { v.RelearningStepsMinutes = []float64{math.NaN()} },
		"too many steps":                  func(v *Values) { v.LearningStepsMinutes = []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11} },
	} {
		t.Run(name, func(t *testing.T) {
			values := Defaults()
			change(&values)
			if err := values.Validate(); err == nil {
				t.Fatal("accepted invalid settings")
			}
		})
	}
}

func TestValidateAllowsDisablingOptionalPolicies(t *testing.T) {
	values := Defaults()
	values.FastAnswerSeconds = 0
	values.LearningStepsMinutes = []float64{}
	values.RelearningStepsMinutes = []float64{}
	values.PresentationCooldownMinutes = 0
	values.RecentPresentationCount = 0
	values.LearnedShareMin, values.LearnedShareMax = 0, 0
	values.ReinforcementCooldownHours = 0
	values.ReinforcementMaxWordShare = 1
	if err := values.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsJSONRequiresEveryNonNullField(t *testing.T) {
	encoded, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for key, original := range fields {
		for _, missing := range []bool{false, true} {
			if missing {
				delete(fields, key)
			} else {
				fields[key] = json.RawMessage("null")
			}
			incomplete, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var values Values
			if err := json.Unmarshal(incomplete, &values); err == nil {
				t.Fatalf("accepted missing=%t field %s", missing, key)
			}
			fields[key] = original
		}
	}
	values := Defaults()
	values.FastAnswerSeconds = 0
	values.RecentPresentationCount = 0
	values.PresentationCooldownMinutes = 0
	values.LearnedShareMin, values.LearnedShareMax = 0, 0
	values.ReinforcementCooldownHours = 0
	values.LearningStepsMinutes, values.RelearningStepsMinutes = []float64{}, []float64{}
	encoded, err = json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Values
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("explicit zero controls rejected: %v", err)
	}
}

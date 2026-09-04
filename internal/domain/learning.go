package domain

// ReviewRating is the learner's self-assessment for one completed review.
type ReviewRating string

const (
	ReviewRatingAgain ReviewRating = "again"
	ReviewRatingHard  ReviewRating = "hard"
	ReviewRatingGood  ReviewRating = "good"
	ReviewRatingEasy  ReviewRating = "easy"
)

func (rating ReviewRating) Valid() bool {
	switch rating {
	case ReviewRatingAgain, ReviewRatingHard, ReviewRatingGood, ReviewRatingEasy:
		return true
	default:
		return false
	}
}

// ExerciseMode identifies an independently scheduled way of testing an item.
type ExerciseMode string

const ExerciseModeProduction ExerciseMode = "production"

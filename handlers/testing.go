package handlers

// This file contains helpers for unit testing this package.
import (
	"time"

	"github.com/kiali/kiali/util"
)

func mockClock() {
	clockTime := time.Date(2017, 0o1, 15, 0, 0, 0, 0, time.UTC)
	util.Clock = util.ClockMock{Time: clockTime}
}

func combineSlices[T any](slices ...[]T) []T {
	var combined []T
	for _, slice := range slices {
		combined = append(combined, slice...)
	}
	return combined
}

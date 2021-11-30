package observability_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kiali/kiali/observability"
)

func TestInitTracer(t *testing.T) {
	assert := assert.New(t)
	defer func() {
		err := recover()
		assert.Nil(err)
	}()
	tp := observability.InitTracer("jaegerURL")
	assert.NotNil(tp)
}

func TestStop(t *testing.T) {
	tp := observability.InitTracer("jaegerURL")
	observability.StopTracer(tp)
}

func TestStopWithNil(t *testing.T) {
	assert := assert.New(t)
	defer func() {
		err := recover()
		assert.Nil(err)
	}()
	observability.StopTracer(nil)
}

package tracing_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kiali/kiali/tracing"
)

func TestInitTracer(t *testing.T) {
	assert := assert.New(t)
	defer func() {
		err := recover()
		assert.Nil(err)
	}()
	tp := tracing.InitTracer("jaegerURL")
	assert.NotNil(tp)
}

func TestStop(t *testing.T) {
	tp := tracing.InitTracer("jaegerURL")
	tracing.Stop(tp)
}

func TestStopWithNil(t *testing.T) {
	assert := assert.New(t)
	defer func() {
		err := recover()
		assert.Nil(err)
	}()
	tracing.Stop(nil)
}

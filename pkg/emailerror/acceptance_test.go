package emailerror

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreAcceptanceErrorSurvivesWrapping(t *testing.T) {
	base := errors.New("smtp rejected RCPT")
	marked := BeforeAcceptance(base)
	require.True(t, IsBeforeAcceptance(fmt.Errorf("send failed: %w", marked)))
	require.False(t, IsBeforeAcceptance(errors.New("ambiguous DATA timeout")))
}

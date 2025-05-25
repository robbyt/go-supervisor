package networking

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		expectedOutput string
		expectedError  error
	}{
		{
			name:           "valid colon port",
			input:          ":8080",
			expectedOutput: ":8080",
			expectedError:  nil,
		},
		{
			name:           "valid port without colon",
			input:          "8080",
			expectedOutput: ":8080",
			expectedError:  nil,
		},
		{
			name:           "valid host port",
			input:          "localhost:8080",
			expectedOutput: "localhost:8080",
			expectedError:  nil,
		},
		{
			name:           "valid IP port",
			input:          "127.0.0.1:8080",
			expectedOutput: "127.0.0.1:8080",
			expectedError:  nil,
		},
		{
			name:           "maximum valid port",
			input:          ":65535",
			expectedOutput: ":65535",
			expectedError:  nil,
		},
		{
			name:           "minimum valid port",
			input:          ":1",
			expectedOutput: ":1",
			expectedError:  nil,
		},
		{
			name:           "empty string",
			input:          "",
			expectedOutput: "",
			expectedError:  ErrEmptyPort,
		},
		{
			name:           "just colon",
			input:          ":",
			expectedOutput: "",
			expectedError:  ErrInvalidFormat,
		},
		{
			name:           "port out of range (too high)",
			input:          ":65536",
			expectedOutput: "",
			expectedError:  ErrPortOutOfRange,
		},
		{
			name:           "port out of range (too low)",
			input:          ":0",
			expectedOutput: "",
			expectedError:  ErrPortOutOfRange,
		},
		{
			name:           "port out of range (negative)",
			input:          ":-1",
			expectedOutput: "",
			expectedError:  ErrInvalidFormat,
		},
		{
			name:           "non-numeric port",
			input:          ":abc",
			expectedOutput: "",
			expectedError:  ErrInvalidFormat,
		},
		{
			name:           "invalid format with multiple colons",
			input:          "localhost:8080:8081",
			expectedOutput: "",
			expectedError:  ErrInvalidFormat,
		},
		{
			name:           "mixed numeric and letters in port",
			input:          ":8080a",
			expectedOutput: "",
			expectedError:  ErrInvalidFormat,
		},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			output, err := ValidatePort(tt.input)

			if tt.expectedError != nil {
				assert.Error(t, err)
				if !errors.Is(tt.expectedError, ErrInvalidFormat) {
					// For specific errors, check exact match
					assert.ErrorIs(t, err, tt.expectedError)
				} else {
					// For format errors, just check that it's a format error (details may vary)
					assert.True(t, errors.Is(err, ErrInvalidFormat), "Expected error to be a ErrInvalidFormat")
				}
				assert.Empty(t, output)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedOutput, output)
			}
		})
	}
}

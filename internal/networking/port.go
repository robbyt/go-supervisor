package networking

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Common port validation errors
var (
	ErrEmptyPort      = errors.New("port cannot be empty")
	ErrInvalidFormat  = errors.New("invalid port format")
	ErrPortOutOfRange = errors.New("port number must be between 1 and 65535")
)

// ValidatePort checks if the provided port string is valid and returns a normalized version.
// It accepts formats like ":8080", "localhost:8080", "127.0.0.1:8080", or "8080".
// Returns the normalized port format (":PORT") and an error if the port is invalid.
func ValidatePort(portStr string) (string, error) {
	if portStr == "" {
		return "", ErrEmptyPort
	}

	// Check for negative numbers in the input before processing
	if strings.Contains(portStr, ":-") ||
		(strings.HasPrefix(portStr, "-") && !strings.Contains(portStr, ":")) {
		return "", fmt.Errorf("%w: negative port numbers are not allowed", ErrInvalidFormat)
	}

	// If it's a number without a colon, add a colon prefix
	if !strings.Contains(portStr, ":") {
		portStr = ":" + portStr
	}

	// Handle host:port format by extracting the port
	host, port, err := net.SplitHostPort(portStr)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidFormat, err)
	}

	// Validate port is a number
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return "", fmt.Errorf("%w: port must be a number", ErrInvalidFormat)
	}

	// Check port range
	if portNum < 1 || portNum > 65535 {
		return "", ErrPortOutOfRange
	}

	// Return normalized form
	if host == "" {
		return ":" + port, nil
	}
	return host + ":" + port, nil
}

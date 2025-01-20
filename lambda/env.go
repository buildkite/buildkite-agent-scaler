package main

import (
	"log"
	"os"
	"strconv"
	"time"
)

// RequireEnvString reads an environment variable, and if it is not set, calls
// [log.Fatalf].
func RequireEnvString(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("%s is required", name)
	}
	return v
}

// RequireEnvInt reads an environment variable and parses it as a decimal int.
// If it is not set or does not parse, it calls [log.Fatalf].
func RequireEnvInt(name string) int {
	v := RequireEnvString(name)
	i, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("%s must be an integer: %v", name, err)
	}
	return i
}

// EnvString reads an environment variable, and if it is not set, returns def.
func EnvString(name, def string) string {
	v := os.Getenv(name)
	if v == "" {
		return def
	}

	return v
}

// EnvInt reads an environment variable, and if it is set, parses it with
// [strconv.Atoi].If it is not set, it returns def. If it does not parse, it calls
// [log.Fatalf].
func EnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("%s must be an integer: %v", name, err)
	}
	return i
}

// EnvDuration reads an environment variable, and if it is set, parses it as a
// [time.Duration]. If it is not set, it returns def. If parsing fails, it calls
// [log.Fatalf].
func EnvDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Fatalf("%s must be a duration: %v", name, err)
	}
	return d
}

// EnvFloat reads an environment variable, and if it is set, parses it using
// [strconv.ParseFloat]. If it is not set, it returns 0. If parsing fails, it
// calls [log.Fatalf].
func EnvFloat(name string) float64 {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Fatalf("%s must be a number: %v", name, err)
	}
	return f
}

// EnvBool reads an environment variable, and if it is set, parses it using
// [strconv.ParseBool]. If it is not set, it returns false. If parsing fails, it
// calls [log.Fatalf].
func EnvBool(name string) bool {
	v := os.Getenv(name)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Fatalf("%s must be a boolean: %v", name, err)
	}
	return b
}

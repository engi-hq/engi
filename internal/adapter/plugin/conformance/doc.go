// Package conformance holds cross-SDK conformance tests for Engi's plugin
// system. The tests (build tag "integration") start the example plugin of each
// SDK (Go, Python, TypeScript, .NET) through the real host adapters and check
// that they behave the same. See harness_test.go for the toolchain handling.
package conformance

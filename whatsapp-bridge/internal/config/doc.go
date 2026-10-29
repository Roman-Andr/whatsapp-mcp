// Package config reads the bridge's settings from the environment, and the
// files the environment names (allowlist files, the welcome file), into one
// Config. Load validates in the order the bridge always did and returns the
// first error with the text it always had: those texts are the start-up
// errors users see.
//
// Load has no side effects. Creating directories, generating secrets
// (Secret) and opening files belong to the constructors that use the
// values. Only this package and main read the environment.
package config

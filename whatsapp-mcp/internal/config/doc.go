// Package config reads the server's settings from the environment and the
// command line, once, at start-up.
//
// It owns every os.Getenv of the server. The files the environment names
// (WHATSAPP_ACCOUNTS_FILE, WHATSAPP_READ_ALLOWLIST_FILE, store/api_token)
// are not read here: their readers re-read them on every call, so editing
// them takes effect without a restart.
package config

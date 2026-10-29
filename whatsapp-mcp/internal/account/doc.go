// Package account knows the configured WhatsApp accounts: each one's store
// directory, bridge API URL and API token.
//
// The account list (WHATSAPP_ACCOUNTS_FILE) and the bridge token
// (store/api_token) are re-read on every call, so edits take effect
// without a restart. It does not open databases or talk to bridges.
package account

// Package storetest builds bridge stores for tests: messages.db with the
// real bridge schema (a copy of testdata/golden/bridge-schema.sql) and the
// shared fixture rows, whatsapp.db with contacts and the LID map, and the
// settings that make the store the default account. It also has a fake
// bridge that records the requests it gets.
//
// It is a normal package so the tests of every other package can use it;
// production code never imports it.
package storetest

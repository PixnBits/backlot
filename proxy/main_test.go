package main

import "testing"

func TestHostOf(t *testing.T) {
	if hostOf("example.com:443") != "example.com" {
		t.Fatal(hostOf("example.com:443"))
	}
	if hostOf("https://Accounts.Google.com/x") != "accounts.google.com" {
		t.Fatal(hostOf("https://Accounts.Google.com/x"))
	}
}

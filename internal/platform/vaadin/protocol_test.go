package vaadin_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/platform/vaadin"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestDecodeBootstrap(test *testing.T) {
	test.Run("should decode nested JSON when bootstrap contains script delimiters in strings", func(test *testing.T) {
		// Given
		data := []byte(`<script>var uidl = {"syncId":0,"clientId":2,"Vaadin-Security-Key":"example-key","changes":[{"node":1,"type":"put","feat":1,"key":"value","value":{"nested":["}; var nested ="]}}]};var config = {"v-uiId":42};</script>`)

		// When
		bootstrap, err := vaadin.DecodeBootstrap(data)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, uint64(42), bootstrap.UIID)
		assert.Equal(test, uint64(0), bootstrap.Message.SyncID)
		assert.Equal(test, uint64(2), bootstrap.Message.ClientID)
		assert.Equal(test, "example-key", *bootstrap.Message.SecurityKey)
		assert.Equal(test, 1, len(bootstrap.Message.Changes))
	})
	for _, scenario := range []struct{ name, data string }{
		{name: "missing markers", data: `<html>example-secret</html>`},
		{name: "malformed uidl", data: `var uidl = {example-secret};var config = {"v-uiId":1}`},
		{name: "null uidl", data: `var uidl = null;var config = {"v-uiId":1}`},
		{name: "null config", data: `var uidl = {"syncId":0,"clientId":0};var config = null`},
		{name: "missing UI ID", data: `var uidl = {"syncId":0,"clientId":0};var config = {}`},
		{name: "string UI ID", data: `var uidl = {"syncId":0,"clientId":0};var config = {"v-uiId":"example-secret"}`},
		{name: "fractional UI ID", data: `var uidl = {"syncId":0,"clientId":0};var config = {"v-uiId":1.5}`},
		{name: "negative UI ID", data: `var uidl = {"syncId":0,"clientId":0};var config = {"v-uiId":-1}`},
		{name: "unsafe UI ID", data: `var uidl = {"syncId":0,"clientId":0};var config = {"v-uiId":9007199254740992}`},
		{name: "invalid counters", data: `var uidl = {"syncId":null,"clientId":0};var config = {"v-uiId":1}`},
	} {
		test.Run("should reject bootstrap when "+scenario.name, func(test *testing.T) {
			// Given
			data := []byte(scenario.data)

			// When
			_, err := vaadin.DecodeBootstrap(data)

			// Then
			assert.True(test, err != nil)
			assert.True(test, !strings.Contains(err.Error(), "example-secret"))
		})
	}
}

func TestDecodeResponse(test *testing.T) {
	for _, prefix := range []string{"", "for(;;);"} {
		test.Run(fmt.Sprintf("should preserve messages and optional metadata when prefix is %q", prefix), func(test *testing.T) {
			// Given
			data := []byte(prefix + `[{"syncId":9007199254740991,"clientId":3,"Vaadin-Security-Key":"example-key"},{"syncId":1,"clientId":0,"changes":[]}]`)

			// When
			messages, err := vaadin.DecodeResponse(data)

			// Then
			assert.NoError(test, err)
			assert.Equal(test, 2, len(messages))
			assert.Equal(test, uint64(9007199254740991), messages[0].SyncID)
			assert.Equal(test, uint64(3), messages[0].ClientID)
			assert.Equal(test, "example-key", *messages[0].SecurityKey)
			assert.Equal(test, uint64(1), messages[1].SyncID)
			assert.Equal(test, uint64(0), messages[1].ClientID)
			assert.True(test, messages[1].SecurityKey == nil)
			assert.Equal(test, 0, len(messages[1].Changes))
		})
	}
	for _, payload := range []string{
		`example-secret`, `[]`, `null`, `[null]`, `{}`, `[{}]`,
		`[{"syncId":0,"clientId":0}] trailing-example-secret`,
		`[{"syncId":-1,"clientId":0}]`, `[{"syncId":1.5,"clientId":0}]`,
		`[{"syncId":"example-secret","clientId":0}]`, `[{"syncId":0,"clientId":null}]`,
		`[{"syncId":9007199254740992,"clientId":0}]`, `[{"syncId":0,"clientId":1e999}]`,
		`[{"syncId":0,"clientId":0,"changes":null}]`, `[{"syncId":0,"clientId":0,"changes":{}}]`,
		`[{"syncId":0,"clientId":0,"changes":[null]}]`, `[{"syncId":0,"clientId":0,"changes":[4]}]`,
		`[{"syncId":0,"clientId":0,"Vaadin-Security-Key":false}]`,
	} {
		test.Run("should reject response when envelope is malformed: "+payload, func(test *testing.T) {
			// Given
			data := []byte(payload)

			// When
			messages, err := vaadin.DecodeResponse(data)

			// Then
			assert.True(test, err != nil)
			assert.True(test, messages == nil)
			assert.True(test, !strings.Contains(err.Error(), "example-secret"))
		})
	}
}

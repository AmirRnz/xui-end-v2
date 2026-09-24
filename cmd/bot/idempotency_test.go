package main

import (
	"strings"
	"testing"
)

func TestStableKeyTracksTelegramMessageAndOperation(t *testing.T) {
	first := stableKey(41, 9, 120, "purchase")
	if first != stableKey(41, 9, 120, "purchase") {
		t.Fatal("same Telegram update must use the same idempotency key")
	}
	if first == stableKey(41, 9, 121, "purchase") {
		t.Fatal("different Telegram messages must not share an idempotency key")
	}
	if first == stableKey(41, 9, 120, "trial") {
		t.Fatal("different operations must not share an idempotency key")
	}
}

func TestFeatureGateDefaultsEnabledAndHonorsDisabledFlags(t *testing.T) {
	if !featureEnabled(publicFeatures{}, "purchases_enabled") {
		t.Fatal("an unset feature must retain the backend's enabled-by-default behavior")
	}
	if featureEnabled(publicFeatures{Features: map[string]bool{"purchases_enabled": false}}, "purchases_enabled") {
		t.Fatal("an explicitly disabled feature must be hidden")
	}
}

func TestPlanFieldCallbackPayloadFitsTelegramLimit(t *testing.T) {
	fields := []string{"name", "kind", "description", "base_price_toman", "price_per_extra_ip_toman", "price_per_gb_toman", "price_per_extra_month_toman", "ip_limits", "min_data_gb", "max_data_bytes", "expire_seconds", "test_ip_limit", "flow", "inbound_ids", "usage_description"}
	for _, field := range fields {
		code := planFieldCode(field)
		decoded, ok := planFieldFromCode(code)
		if !ok || decoded != field {
			t.Fatalf("field %q did not round trip through callback code %q", field, code)
		}
		payload := strings.Join([]string{"0123456789", "pf", "9223372036854775807", code}, "|")
		if len(payload) > 64 {
			t.Fatalf("callback payload for %q exceeds Telegram's 64-byte limit: %d", field, len(payload))
		}
	}
}

func TestPaymentInstructionPatchPreservesOtherFields(t *testing.T) {
	current := map[string]string{"card_number": "1111", "card_owner": "Existing owner", "instructions": "Pay exact amount"}
	updated := paymentInstructionPatch(current, "card_number", "2222")
	if updated["card_number"] != "2222" || updated["card_owner"] != "Existing owner" || updated["instructions"] != "Pay exact amount" {
		t.Fatalf("updating one payment field must preserve the other configured fields: %#v", updated)
	}
}

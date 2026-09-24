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

func TestSessionOperationKeyDeduplicatesDeliveryButSeparatesActions(t *testing.T) {
	same := sessionKey(41, 9, 120, "menu-a", "trial", "7")
	if same != sessionKey(41, 9, 120, "menu-a", "trial", "7") {
		t.Fatal("redelivery of the same action must reuse its idempotency key")
	}
	if same == sessionKey(41, 9, 120, "menu-b", "trial", "7") {
		t.Fatal("a new menu session must get a distinct idempotency key")
	}
	if same == sessionKey(41, 9, 120, "menu-a", "trial", "8") {
		t.Fatal("a different target must get a distinct idempotency key")
	}
	if same == sessionKey(41, 9, 120, "menu-a", "cancel", "7") {
		t.Fatal("a different operation must get a distinct idempotency key")
	}
}

func TestSubscriptionPagesExposeEveryLinkAndEmptyLinkSubscription(t *testing.T) {
	subscriptions := []subscriptionView{
		{ID: 10, Links: []string{"vless://one", "https://example.test/sub"}},
		{ID: 11},
	}
	pages := subscriptionLinkPages(subscriptions)
	if len(pages) != 3 {
		t.Fatalf("expected one page per connection link and one empty subscription page, got %d", len(pages))
	}
	if pages[0].Subscription.ID != 10 || pages[0].LinkIndex != 0 || pages[1].Subscription.ID != 10 || pages[1].LinkIndex != 1 || pages[2].Subscription.ID != 11 || pages[2].LinkIndex != -1 {
		t.Fatalf("subscription links were dropped or misordered: %#v", pages)
	}
}

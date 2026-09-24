package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/xui-end-bot-v2/internal/backend"
	"gopkg.in/telebot.v3"
)

func testBackend(t *testing.T, handler http.HandlerFunc) (*backend.Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(handler)
	api, err := backend.New(s.URL, "test-token", time.Second)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	api.HTTP = s.Client()
	return api, s
}

func TestRetailHomeMatchesLegacyMenuSnapshot(t *testing.T) {
	api, backendServer := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/actors/resolve":
			_, _ = w.Write([]byte(`{"telegram_id":42,"role":"customer","approval_status":"approved"}`))
		case "/v1/features":
			_, _ = w.Write([]byte(`{"features":{},"text":{}}`))
		default:
			t.Errorf("unexpected backend request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	})
	defer backendServer.Close()
	app := &botApp{api: api, states: make(map[int64]conversation)}
	bot, err := telebot.NewBot(telebot.Settings{Offline: true, Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := bot.NewContext(telebot.Update{Message: &telebot.Message{
		Sender: &telebot.User{ID: 42}, Chat: &telebot.Chat{ID: 42, Type: telebot.ChatPrivate}, Text: "/start",
	}})
	text, markup, err := app.homeView(ctx, "👋 به پنل کاربری خوش آمدید\nسرویس وی‌پی‌ان خود را مدیریت کنید یا سرویس جدید خریداری نمایید.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "👋 به پنل کاربری خوش آمدید") {
		t.Fatalf("unexpected legacy welcome text: %q", text)
	}
	want := [][]string{
		{"🧪 تست رایگان", "💼 خرید سرویس"},
		{"📋 سرویس‌های من", "👛 کیف پول"},
		{"🆘 پشتیبانی"},
	}
	if len(markup.InlineKeyboard) != len(want) {
		t.Fatalf("home rows = %d, want %d", len(markup.InlineKeyboard), len(want))
	}
	for row, labels := range want {
		if len(markup.InlineKeyboard[row]) != len(labels) {
			t.Fatalf("row %d button count = %d, want %d", row, len(markup.InlineKeyboard[row]), len(labels))
		}
		for col, label := range labels {
			if got := markup.InlineKeyboard[row][col].Text; got != label {
				t.Fatalf("button [%d,%d] = %q, want %q", row, col, got, label)
			}
		}
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if strings.Contains(strings.ToLower(button.Text), "admin") || strings.Contains(button.Text, "مدیریت") {
				t.Fatalf("admin control leaked onto customer home: %q", button.Text)
			}
		}
	}
}

func TestTelebotDispatchesLegacyPurchaseMenuCallback(t *testing.T) {
	var planRequest bool
	api, backendServer := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/actors/resolve":
			_, _ = w.Write([]byte(`{"telegram_id":42,"role":"customer","approval_status":"approved"}`))
		case "/v1/features":
			_, _ = w.Write([]byte(`{"features":{},"text":{}}`))
		case "/v1/plans":
			planRequest = r.URL.Query().Get("kind") == "paid"
			_, _ = w.Write([]byte(`[{"id":7,"name":"ماهیانه","kind":"paid","is_limited":false,"base_price_toman":120000,"base_ip_limit":1,"max_ip_limit":3}]`))
		default:
			t.Errorf("unexpected backend request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	})
	defer backendServer.Close()

	var editSeen bool
	telegramServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/editMessageText") {
			editSeen = true
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":99,"date":1,"chat":{"id":42,"type":"private"},"text":"ok"}}`)
	}))
	defer telegramServer.Close()
	bot, err := telebot.NewBot(telebot.Settings{Token: "test-token", URL: telegramServer.URL, Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	app := &botApp{api: api, states: make(map[int64]conversation)}
	app.register(bot)
	app.setState(42, conversation{})
	nonce := app.state(42).Nonce
	bot.ProcessUpdate(telebot.Update{Callback: &telebot.Callback{
		ID: "callback-1", Sender: &telebot.User{ID: 42}, Data: "\fnav|" + nonce + "|plans-paid",
		Message: &telebot.Message{ID: 99, Sender: &telebot.User{ID: 0}, Chat: &telebot.Chat{ID: 42, Type: telebot.ChatPrivate}},
	}})
	if !planRequest {
		t.Fatal("real Telebot callback dispatch did not reach the paid plans backend route")
	}
	if !editSeen {
		t.Fatal("paid plans callback did not edit the current inline-menu message")
	}
	if _, ok := app.consumeCallbackState(42, nonce); ok {
		t.Fatal("callback nonce was not consumed by the real registered handler")
	}
}

func TestRetailHomeHidesBackendDisabledLegacyActions(t *testing.T) {
	api, backendServer := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/actors/resolve" {
			_, _ = w.Write([]byte(`{"telegram_id":42,"role":"customer"}`))
			return
		}
		_, _ = w.Write([]byte(`{"features":{"purchases_enabled":false,"trials_enabled":false,"wallet_enabled":false},"text":{}}`))
	})
	defer backendServer.Close()
	app := &botApp{api: api, states: make(map[int64]conversation)}
	bot, err := telebot.NewBot(telebot.Settings{Offline: true, Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := bot.NewContext(telebot.Update{Message: &telebot.Message{Sender: &telebot.User{ID: 42}, Chat: &telebot.Chat{ID: 42, Type: telebot.ChatPrivate}}})
	_, markup, err := app.homeView(ctx, "welcome")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(markup.InlineKeyboard)
	if strings.Contains(string(data), "plans-paid") || strings.Contains(string(data), "plans-test") || strings.Contains(string(data), "wallet") {
		t.Fatalf("disabled retail features appeared on home: %s", data)
	}
}

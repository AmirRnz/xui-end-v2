package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"example.com/xui-end-bot-v2/internal/backend"
	"gopkg.in/telebot.v3"
)

type actor struct {
	TelegramID     int64  `json:"telegram_id"`
	Role           string `json:"role"`
	ApprovalStatus string `json:"approval_status"`
}
type plan struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	IsLimited     bool   `json:"is_limited"`
	BasePrice     int64  `json:"base_price_toman"`
	PriceGB       int64  `json:"price_per_gb_toman"`
	MinGB         int    `json:"min_data_gb"`
	BaseIP        int    `json:"base_ip_limit"`
	MaxIP         int    `json:"max_ip_limit"`
	MaxBytes      int64  `json:"max_data_bytes"`
	ExpireSeconds int64  `json:"expire_seconds"`
}
type quote struct {
	ID       int64  `json:"id"`
	Price    int64  `json:"final_price_toman"`
	Currency string `json:"currency"`
}
type purchase struct {
	OrderID        int64  `json:"order_id"`
	Status         string `json:"status"`
	IntentID       int64  `json:"payment_intent_id"`
	SubscriptionID int64  `json:"subscription_id"`
	Amount         int64  `json:"amount_toman"`
}
type receiptState struct {
	Kind string
	ID   int64
}
type botApp struct {
	api     *backend.Client
	receipt sync.Map
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	base := strings.TrimSpace(os.Getenv("BACKEND_URL"))
	secret := os.Getenv("BACKEND_TOKEN")
	if token == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	timeout := 20 * time.Second
	if raw := os.Getenv("BACKEND_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			timeout = d
		}
	}
	api, err := backend.New(base, secret, timeout)
	if err != nil {
		return err
	}
	b, err := telebot.NewBot(telebot.Settings{Token: token, Poller: &telebot.LongPoller{Timeout: 10 * time.Second}})
	if err != nil {
		return err
	}
	app := &botApp{api: api}
	app.register(b)
	done := make(chan struct{})
	go func() { b.Start(); close(done) }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
		b.Stop()
		<-done
	case <-done:
	}
	return nil
}
func (a *botApp) register(b *telebot.Bot) {
	b.Handle("/start", func(c telebot.Context) error {
		act, err := a.resolve(c)
		if err != nil {
			return sendFailure(c, err)
		}
		return c.Send(fmt.Sprintf("سلام. حساب شما به backend مشترک متصل است. وضعیت حساب: %s. برای راهنما /help را بفرستید.", act.ApprovalStatus))
	})
	b.Handle("/help", func(c telebot.Context) error {
		return c.Send("دستورها:\n/plans — طرح‌های خرید\n/plans test — طرح‌های تست\n/trial <plan_id> — یک تست\n/buy <plan_id> <ماه> <IP> <GB> [نام] — پرداخت از کیف پول\n/pay <plan_id> <ماه> <IP> <GB> [نام] — پرداخت مستقیم\n/wallet و /ledger\n/topup <تومان> — درخواست شارژ\n/receipt <payment_intent_id> و سپس ارسال عکس رسید\n/topupreceipt <topup_id> و سپس ارسال عکس رسید\n/services و /cancel <subscription_id>")
	})
	b.Handle("/plans", a.plans)
	b.Handle("/trial", a.trial)
	b.Handle("/buy", func(c telebot.Context) error { return a.buy(c, "wallet") })
	b.Handle("/pay", func(c telebot.Context) error { return a.buy(c, "direct") })
	b.Handle("/wallet", a.wallet)
	b.Handle("/ledger", a.ledger)
	b.Handle("/topup", a.topup)
	b.Handle("/receipt", func(c telebot.Context) error { return a.startReceipt(c, "payment") })
	b.Handle("/topupreceipt", func(c telebot.Context) error { return a.startReceipt(c, "topup") })
	b.Handle("/services", a.services)
	b.Handle("/cancel", a.cancel)
	b.Handle("/pending", a.pending)
	b.Handle("/approve", a.approve)
	b.Handle("/pendingtopups", a.pendingTopups)
	b.Handle("/approvetopup", a.approveTopup)
	b.Handle(telebot.OnPhoto, a.photo)
}
func (a *botApp) resolve(c telebot.Context) (actor, error) {
	id := c.Sender().ID
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out actor
	err := a.api.Call(ctx, "POST", "/v1/actors/resolve", 0, map[string]any{"telegram_id": id}, &out)
	return out, err
}
func (a *botApp) plans(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	kind := "paid"
	if strings.Contains(strings.ToLower(c.Message().Text), "test") {
		kind = "test"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var items []plan
	if err = a.api.Call(ctx, "GET", "/v1/plans?kind="+kind, act.TelegramID, nil, &items); err != nil {
		return sendFailure(c, err)
	}
	if len(items) == 0 {
		return c.Send("در حال حاضر طرحی در دسترس نیست.")
	}
	var b strings.Builder
	for _, p := range items {
		if kind == "test" {
			fmt.Fprintf(&b, "🧪 %d — %s | مدت: %d ثانیه پس از اولین اتصال | حجم: %d بایت\n", p.ID, p.Name, p.ExpireSeconds, p.MaxBytes)
		} else if p.IsLimited {
			fmt.Fprintf(&b, "📦 %d — %s | هر گیگابایت: %d تومان | حداقل %dGB | IP: %d تا %d\n", p.ID, p.Name, p.PriceGB, p.MinGB, p.BaseIP, p.MaxIP)
		} else {
			fmt.Fprintf(&b, "📦 %d — %s | پایه: %d تومان/ماه | IP: %d تا %d\n", p.ID, p.Name, p.BasePrice, p.BaseIP, p.MaxIP)
		}
	}
	return c.Send(strings.TrimSpace(b.String()))
}
func (a *botApp) trial(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	args := arguments(c)
	if len(args) != 1 {
		return c.Send("استفاده: /trial <plan_id>")
	}
	planID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || planID <= 0 {
		return c.Send("شناسه طرح نامعتبر است.")
	}
	key := stableKey(c.Sender().ID, c.Chat().ID, int64(c.Message().ID), "trial")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out purchase
	err = a.api.Call(ctx, "POST", "/v1/trials", act.TelegramID, map[string]any{"plan_id": planID, "idempotency_key": key}, &out)
	if err != nil {
		return sendFailure(c, err)
	}
	return c.Send("درخواست تست به صورت پایدار ثبت شد؛ فعال‌سازی پس از تأیید پنل ادامه می‌یابد.")
}
func (a *botApp) buy(c telebot.Context, method string) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	args := arguments(c)
	if len(args) < 4 || len(args) > 5 {
		return c.Send("استفاده: /buy یا /pay <plan_id> <ماه> <IP> <GB> [نام]. برای طرح نامحدود GB را صفر بگذارید.")
	}
	planID, e1 := strconv.ParseInt(args[0], 10, 64)
	months, e2 := strconv.Atoi(args[1])
	ip, e3 := strconv.Atoi(args[2])
	gb, e4 := strconv.Atoi(args[3])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return c.Send("پارامترهای خرید نامعتبر است.")
	}
	name := ""
	if len(args) == 5 {
		name = args[4]
	}
	key := stableKey(c.Sender().ID, c.Chat().ID, int64(c.Message().ID), "purchase")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var q quote
	err = a.api.Call(ctx, "POST", "/v1/quotes", act.TelegramID, map[string]any{"plan_id": planID, "months": months, "ip_limit": ip, "data_gb": gb, "idempotency_key": "quote-" + key}, &q)
	if err != nil {
		return sendFailure(c, err)
	}
	var out purchase
	err = a.api.Call(ctx, "POST", "/v1/purchases", act.TelegramID, map[string]any{"quote_id": q.ID, "payment_method": method, "idempotency_key": "purchase-" + key, "display_name": name}, &out)
	if err != nil {
		return sendFailure(c, err)
	}
	if method == "wallet" {
		return c.Send(fmt.Sprintf("درخواست خرید ثبت شد. مبلغ %d تومان. وضعیت: %s.", out.Amount, out.Status))
	}
	var instruction map[string]string
	if err = a.api.Call(ctx, "GET", "/v1/payment-instructions", act.TelegramID, nil, &instruction); err != nil {
		return sendFailure(c, err)
	}
	text := fmt.Sprintf("فاکتور مستقیم شماره %d\nمبلغ: %d تومان\nشماره کارت: %s\nصاحب کارت: %s\n%s\nپس از پرداخت /receipt %d را بفرستید و عکس رسید را ارسال کنید.", out.IntentID, out.Amount, instruction["card_number"], instruction["card_owner"], instruction["instructions"], out.IntentID)
	return c.Send(text)
}
func (a *botApp) wallet(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/wallet", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	return c.Send(fmt.Sprintf("موجودی کیف پول: %v تومان", out["balance_toman"]))
}
func (a *botApp) ledger(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/wallet/ledger", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	if len(out) == 0 {
		return c.Send("تراکنشی ثبت نشده است.")
	}
	var b strings.Builder
	for _, row := range out {
		fmt.Fprintf(&b, "%v تومان | %v | %v\n", row["amount_toman"], row["type"], row["description"])
	}
	return c.Send(strings.TrimSpace(b.String()))
}
func (a *botApp) topup(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	args := arguments(c)
	if len(args) != 1 {
		return c.Send("استفاده: /topup <مبلغ به تومان>")
	}
	amount, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return c.Send("مبلغ نامعتبر است.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	err = a.api.Call(ctx, "POST", "/v1/wallet/topups", act.TelegramID, map[string]any{"amount_toman": amount, "idempotency_key": stableKey(c.Sender().ID, c.Chat().ID, int64(c.Message().ID), "topup")}, &out)
	if err != nil {
		return sendFailure(c, err)
	}
	return c.Send(fmt.Sprintf("درخواست شارژ شماره %v ثبت شد. پس از واریز /topupreceipt %v را بفرستید و عکس رسید را ارسال کنید.", out["topup_id"], out["topup_id"]))
}
func (a *botApp) startReceipt(c telebot.Context, kind string) error {
	args := arguments(c)
	if len(args) != 1 {
		return c.Send("شناسه درخواست را وارد کنید.")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return c.Send("شناسه نامعتبر است.")
	}
	a.receipt.Store(c.Sender().ID, receiptState{kind, id})
	return c.Send("حالا عکس رسید را ارسال کنید.")
}
func (a *botApp) photo(c telebot.Context) error {
	stateValue, ok := a.receipt.Load(c.Sender().ID)
	if !ok {
		return c.Send("برای ثبت رسید ابتدا /receipt یا /topupreceipt را همراه شناسه درخواست بفرستید.")
	}
	state := stateValue.(receiptState)
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	msg := c.Message()
	if msg == nil || msg.Photo == nil {
		return c.Send("عکس رسید دریافت نشد.")
	}
	path := fmt.Sprintf("/v1/payment-intents/%d/receipt", state.ID)
	if state.Kind == "topup" {
		path = fmt.Sprintf("/v1/wallet/topups/%d/receipt", state.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = a.api.Call(ctx, "POST", path, act.TelegramID, map[string]any{"telegram_file_id": msg.Photo.FileID}, nil)
	if err != nil {
		return sendFailure(c, err)
	}
	a.receipt.Delete(c.Sender().ID)
	return c.Send("رسید ثبت شد و برای بررسی اپراتور در صف قرار گرفت.")
}
func (a *botApp) services(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/subscriptions", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	if len(out) == 0 {
		return c.Send("اشتراکی ثبت نشده است.")
	}
	var b strings.Builder
	for _, s := range out {
		fmt.Fprintf(&b, "%v — %v — %v — %v\n", s["id"], s["display_name"], s["status"], s["email"])
	}
	return c.Send(strings.TrimSpace(b.String()))
}
func (a *botApp) cancel(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	args := arguments(c)
	if len(args) != 1 {
		return c.Send("استفاده: /cancel <subscription_id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return c.Send("شناسه اشتراک نامعتبر است.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	err = a.api.Call(ctx, "POST", fmt.Sprintf("/v1/subscriptions/%d/cancel", id), act.TelegramID, map[string]any{"idempotency_key": stableKey(c.Sender().ID, c.Chat().ID, int64(c.Message().ID), "cancel")}, &out)
	if err != nil {
		return sendFailure(c, err)
	}
	return c.Send("درخواست لغو ثبت شد و وضعیت پنل در حال تطبیق است.")
}

func (a *botApp) pending(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/admin/payments", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	if len(out) == 0 {
		return c.Send("پرداخت معوقی وجود ندارد.")
	}
	var b strings.Builder
	for _, p := range out {
		fmt.Fprintf(&b, "%v — %v تومان — /approve %v\n", p["id"], p["amount_toman"], p["id"])
	}
	return c.Send(strings.TrimSpace(b.String()))
}
func (a *botApp) approve(c telebot.Context) error {
	return a.adminAction(c, "approve", "/v1/payment-intents/%d/approve")
}
func (a *botApp) pendingTopups(c telebot.Context) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/admin/topups", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	if len(out) == 0 {
		return c.Send("درخواست شارژ معوقی وجود ندارد.")
	}
	var b strings.Builder
	for _, p := range out {
		fmt.Fprintf(&b, "%v — %v تومان — /approvetopup %v\n", p["id"], p["amount_toman"], p["id"])
	}
	return c.Send(strings.TrimSpace(b.String()))
}
func (a *botApp) approveTopup(c telebot.Context) error {
	return a.adminAction(c, "approvetopup", "/v1/wallet/topups/%d/approve")
}

func (a *botApp) adminAction(c telebot.Context, command, path string) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	args := arguments(c)
	if len(args) != 1 {
		return c.Send("استفاده: /" + command + " <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return c.Send("شناسه نامعتبر است.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	if err = a.api.Call(ctx, "POST", fmt.Sprintf(path, id), act.TelegramID, map[string]any{}, &out); err != nil {
		return sendFailure(c, err)
	}
	return c.Send("عملیات ثبت شد: " + fmt.Sprint(out["status"]))
}
func arguments(c telebot.Context) []string {
	if c.Message() == nil {
		return nil
	}
	fields := strings.Fields(c.Message().Text)
	if len(fields) < 2 {
		return nil
	}
	return fields[1:]
}
func stableKey(senderID, chatID, messageID int64, operation string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s", senderID, chatID, messageID, operation)))
	return hex.EncodeToString(sum[:])
}
func sendFailure(c telebot.Context, err error) error {
	return c.Send("درخواست انجام نشد: " + err.Error())
}

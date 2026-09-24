package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

const adminTelegramID int64 = 96937669

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
type subscriptionView struct {
	ID                int64    `json:"id"`
	Email             string   `json:"email"`
	DisplayName       string   `json:"display_name"`
	Status            string   `json:"status"`
	Kind              string   `json:"kind"`
	IPLimit           int      `json:"ip_limit"`
	TrafficLimitBytes int64    `json:"traffic_limit_bytes"`
	ExpiryTimeMS      int64    `json:"expiry_time_ms"`
	Links             []string `json:"links"`
}
type subscriptionLinkPage struct {
	Subscription subscriptionView
	LinkIndex    int
}

func subscriptionLinkPages(subscriptions []subscriptionView) []subscriptionLinkPage {
	pages := make([]subscriptionLinkPage, 0, len(subscriptions))
	for _, sub := range subscriptions {
		hasLink := false
		for i, link := range sub.Links {
			if strings.TrimSpace(link) != "" {
				pages = append(pages, subscriptionLinkPage{sub, i})
				hasLink = true
			}
		}
		if !hasLink {
			pages = append(pages, subscriptionLinkPage{sub, -1})
		}
	}
	return pages
}

type receiptState struct {
	Kind string
	ID   int64
}
type conversation struct {
	Nonce        string
	Step         string
	PlanID       int64
	Method       string
	Months       int
	IPLimit      int
	DataGB       int
	Name         string
	Receipt      *receiptState
	Admin        string
	AdminID      int64
	PanelURL     string
	Draft        *adminPlan
	OperationKey string
	Updated      time.Time
}
type adminPlan struct {
	ID                 int64   `json:"id,omitempty"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`
	Enabled            bool    `json:"enabled"`
	IsLimited          bool    `json:"is_limited"`
	Description        string  `json:"description"`
	BasePrice          int64   `json:"base_price_toman"`
	PricePerExtraIP    int64   `json:"price_per_extra_ip_toman"`
	PricePerGB         int64   `json:"price_per_gb_toman"`
	PricePerExtraMonth int64   `json:"price_per_extra_month_toman"`
	BaseIP             int     `json:"base_ip_limit"`
	MaxIP              int     `json:"max_ip_limit"`
	MinGB              int     `json:"min_data_gb"`
	MaxBytes           int64   `json:"max_data_bytes"`
	ExpireSeconds      int64   `json:"expire_seconds"`
	TestIPLimit        int     `json:"test_ip_limit"`
	MaxPerDay          int     `json:"max_per_day"`
	Flow               string  `json:"flow"`
	InboundIDs         []int64 `json:"inbound_ids"`
	UsageDescription   string  `json:"usage_description"`
}
type adminConfig struct {
	DeploymentID        string            `json:"deployment_id"`
	Channel             string            `json:"channel"`
	Plans               []adminPlan       `json:"plans"`
	PaymentInstructions map[string]string `json:"payment_instructions"`
	Settings            struct {
		RetailTrialResetDays      int               `json:"retail_trial_reset_days"`
		UnapprovedTrialDailyLimit int               `json:"unapproved_trial_daily_limit"`
		Features                  map[string]bool   `json:"features"`
		Text                      map[string]string `json:"text"`
	} `json:"settings"`
	Panel struct {
		ID              string `json:"id"`
		BaseURL         string `json:"base_url"`
		TokenConfigured bool   `json:"token_configured"`
	} `json:"panel"`
}
type publicFeatures struct {
	Features map[string]bool   `json:"features"`
	Text     map[string]string `json:"text"`
}
type botApp struct {
	api    *backend.Client
	mu     sync.Mutex
	states map[int64]conversation
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
	app := &botApp{api: api, states: make(map[int64]conversation)}
	app.register(b)
	// A blank BotFather menu removes stale commands previously published by
	// older builds. Users enter through Telegram's Start affordance or buttons.
	if err := b.DeleteCommands(); err != nil {
		log.Printf("could not clear Telegram command menu: %v", err)
	}
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
	b.Handle("/start", a.start)
	b.Handle("/admin", a.adminCommand)
	registerCallbackRoutes(b, a.callback)
	b.Handle(telebot.OnText, a.text)
	b.Handle(telebot.OnPhoto, a.photo)
}

func registerCallbackRoutes(b *telebot.Bot, handler telebot.HandlerFunc) {
	// Register the button endpoint so telebot decodes its unique and payload
	// before invoking the handler. OnCallback remains a fallback for unknown
	// callback payloads, which are rejected as stale in callback().
	b.Handle(&telebot.Btn{Unique: "nav"}, handler)
	b.Handle(telebot.OnCallback, handler)
}

func newNonce() string {
	var data [5]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(data[:])
}
func (a *botApp) state(user int64) conversation {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.states[user]
	if st.Nonce == "" || time.Since(st.Updated) > 30*time.Minute {
		st = conversation{Nonce: newNonce()}
	}
	return st
}
func (a *botApp) setState(user int64, st conversation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, old := range a.states {
		if old.Updated.Before(cutoff) {
			delete(a.states, id)
		}
	}
	st.Nonce = newNonce()
	st.Updated = time.Now()
	a.states[user] = st
}
func (a *botApp) clearState(user int64) {
	a.mu.Lock()
	delete(a.states, user)
	a.mu.Unlock()
}
func (a *botApp) consumeCallbackState(user int64, nonce string) (conversation, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.states[user]
	if !ok || nonce == "" || st.Nonce != nonce || time.Since(st.Updated) > 30*time.Minute {
		return conversation{}, false
	}
	// Rotate the stored nonce atomically before routing. Duplicate callback
	// deliveries and concurrent taps can therefore consume an action only once.
	consumed := st
	st.Nonce = newNonce()
	st.Updated = time.Now()
	a.states[user] = st
	return consumed, true
}
func (a *botApp) next(st conversation) conversation {
	st.Nonce = newNonce()
	st.Updated = time.Now()
	return st
}
func (a *botApp) resolve(c telebot.Context) (actor, error) {
	id := c.Sender().ID
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out actor
	err := a.api.Call(ctx, "POST", "/v1/actors/resolve", 0, map[string]any{"telegram_id": id}, &out)
	return out, err
}
func (a *botApp) start(c telebot.Context) error {
	a.clearState(c.Sender().ID)
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	return a.home(c, fmt.Sprintf("خوش آمدید. وضعیت حساب: %s", act.ApprovalStatus), false)
}
func (a *botApp) adminCommand(c telebot.Context) error {
	if !adminCommandSender(c.Sender(), c.Chat()) {
		return c.Send("دسترسی مجاز نیست.")
	}
	a.setState(c.Sender().ID, conversation{})
	return a.adminHome(c, false)
}
func adminCommandSender(sender *telebot.User, chat *telebot.Chat) bool {
	return sender != nil && sender.ID == adminTelegramID && chat != nil && chat.Type == telebot.ChatPrivate
}
func isNavCallback(cb *telebot.Callback) bool {
	return cb != nil && cb.Unique == "nav"
}
func (a *botApp) home(c telebot.Context, message string, edit bool) error {
	st := a.next(conversation{})
	a.setState(c.Sender().ID, st)
	text, m, err := a.homeView(c, message)
	if err != nil {
		return sendFailure(c, err)
	}
	return present(c, text, m, edit)
}
func (a *botApp) homeView(c telebot.Context, message string) (string, *telebot.ReplyMarkup, error) {
	st := a.state(c.Sender().ID)
	act, err := a.resolve(c)
	if err != nil {
		return "", nil, err
	}
	features, err := a.getFeaturesForActor(act)
	if err != nil {
		return "", nil, err
	}
	m := &telebot.ReplyMarkup{}
	rows := make([]telebot.Row, 0, 6)
	if featureEnabled(features, "purchases_enabled") {
		rows = append(rows, m.Row(m.Data(menuText(features, "menu_purchases", "🛍 طرح‌های خرید"), "nav", st.Nonce, "plans-paid")))
	}
	if featureEnabled(features, "trials_enabled") {
		rows = append(rows, m.Row(m.Data(menuText(features, "menu_trials", "🧪 طرح‌های تست"), "nav", st.Nonce, "plans-test")))
	}
	if featureEnabled(features, "topups_enabled") {
		rows = append(rows, m.Row(m.Data(menuText(features, "menu_topup", "💳 شارژ کیف پول"), "nav", st.Nonce, "topup")))
	}
	if featureEnabled(features, "wallet_enabled") {
		rows = append(rows, m.Row(m.Data(menuText(features, "menu_wallet", "👛 موجودی"), "nav", st.Nonce, "wallet"), m.Data(menuText(features, "menu_ledger", "📜 تراکنش‌ها"), "nav", st.Nonce, "ledger")))
	}
	rows = append(rows, m.Row(m.Data(menuText(features, "menu_services", "📡 اشتراک‌های من"), "nav", st.Nonce, "services")))
	rows = append(rows, m.Row(m.Data("🔄 خانه", "nav", st.Nonce, "home")))
	m.Inline(rows...)
	if custom := strings.TrimSpace(features.Text["welcome"]); custom != "" && (message == "صفحه اصلی" || strings.HasPrefix(message, "خوش آمدید")) {
		message = custom
	}
	return message, m, nil
}
func (a *botApp) freshHome(c telebot.Context, message string) error {
	a.setState(c.Sender().ID, conversation{})
	text, m, err := a.homeView(c, message)
	if err != nil {
		st := a.state(c.Sender().ID)
		m = &telebot.ReplyMarkup{}
		m.Inline(m.Row(m.Data("🔄 تلاش دوباره", "nav", st.Nonce, "home")))
		text = "منو در دسترس نیست. برای بارگذاری دوباره دکمه زیر را بزنید."
	}
	if err := c.Edit(text, m); err == nil {
		return nil
	}
	return c.Send(text, m)
}
func (a *botApp) getFeatures(c telebot.Context) (publicFeatures, error) {
	act, err := a.resolve(c)
	if err != nil {
		return publicFeatures{}, err
	}
	return a.getFeaturesForActor(act)
}
func (a *botApp) getFeaturesForActor(act actor) (publicFeatures, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out publicFeatures
	err := a.api.Call(ctx, "GET", "/v1/features", act.TelegramID, nil, &out)
	return out, err
}
func featureEnabled(features publicFeatures, key string) bool {
	value, ok := features.Features[key]
	return !ok || value
}
func menuText(features publicFeatures, key, fallback string) string {
	if text := strings.TrimSpace(features.Text[key]); text != "" {
		return text
	}
	return fallback
}
func present(c telebot.Context, message string, markup *telebot.ReplyMarkup, edit bool) error {
	if edit {
		return c.Edit(message, markup)
	}
	return c.Send(message, markup)
}
func (a *botApp) callback(c telebot.Context) error {
	cb := c.Callback()
	if cb == nil || c.Sender() == nil {
		return nil
	}
	if !isNavCallback(cb) {
		if err := c.Respond(&telebot.CallbackResponse{Text: "این گزینه منقضی شد.", ShowAlert: true}); err != nil {
			log.Printf("callback acknowledgement failed")
		}
		return a.freshHome(c, "از منوی تازه ادامه دهید.")
	}
	fields := strings.Split(c.Data(), "|")
	if len(fields) < 2 {
		if err := c.Respond(&telebot.CallbackResponse{Text: "این گزینه منقضی شد.", ShowAlert: true}); err != nil {
			log.Printf("callback acknowledgement failed")
		}
		return a.freshHome(c, "از منوی تازه ادامه دهید.")
	}
	st, ok := a.consumeCallbackState(c.Sender().ID, fields[0])
	if !ok {
		if err := c.Respond(&telebot.CallbackResponse{Text: "صفحه به‌روز شد.", ShowAlert: true}); err != nil {
			log.Printf("callback acknowledgement failed")
		}
		return a.freshHome(c, "این دکمه منقضی شده بود. از منوی تازه ادامه دهید.")
	}
	if err := c.Respond(); err != nil {
		log.Printf("callback acknowledgement failed: %v", err)
	}
	command, args := fields[1], fields[2:]
	return a.route(c, command, args, st)
}
func (a *botApp) route(c telebot.Context, action string, args []string, st conversation) error {
	switch action {
	case "home":
		return a.home(c, "صفحه اصلی", true)
	case "plans-paid":
		features, e := a.getFeatures(c)
		if e != nil {
			return sendFailure(c, e)
		}
		if !featureEnabled(features, "purchases_enabled") {
			return a.home(c, "خرید در حال حاضر غیرفعال است.", true)
		}
		return a.showPlans(c, "paid", true)
	case "plans-test":
		features, e := a.getFeatures(c)
		if e != nil {
			return sendFailure(c, e)
		}
		if !featureEnabled(features, "trials_enabled") {
			return a.home(c, "تست در حال حاضر غیرفعال است.", true)
		}
		return a.showPlans(c, "test", true)
	case "select-paid", "select-test":
		if len(args) != 1 {
			return a.home(c, "درخواست نامعتبر است.", true)
		}
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id <= 0 {
			return a.home(c, "طرح نامعتبر است.", true)
		}
		if action == "select-test" {
			return a.createTrial(c, id)
		}
		st.PlanID = id
		st.OperationKey = callbackOperationKey(c, "purchase", strconv.FormatInt(id, 10))
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		st = a.state(c.Sender().ID)
		m := &telebot.ReplyMarkup{}
		m.Inline(m.Row(m.Data("پرداخت با کیف پول", "nav", st.Nonce, "method-wallet")), m.Row(m.Data("پرداخت مستقیم", "nav", st.Nonce, "method-direct")), m.Row(m.Data("↩️ طرح‌ها", "nav", st.Nonce, "plans-paid")))
		return c.Edit("روش پرداخت را انتخاب کنید.", m)
	case "method-wallet", "method-direct":
		features, e := a.getFeatures(c)
		if e != nil {
			return sendFailure(c, e)
		}
		if !featureEnabled(features, "purchases_enabled") {
			return a.home(c, "خرید در حال حاضر غیرفعال است.", true)
		}
		if action == "method-wallet" && !featureEnabled(features, "wallet_enabled") {
			return a.home(c, "پرداخت با کیف پول غیرفعال است.", true)
		}
		if action == "method-direct" && !featureEnabled(features, "direct_payments_enabled") {
			return a.home(c, "پرداخت مستقیم غیرفعال است.", true)
		}
		st.Method = "wallet"
		if action == "method-direct" {
			st.Method = "direct"
		}
		st.Step = "purchase-months"
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "مدت اشتراک را به ماه وارد کنید.", true)
	case "topup":
		features, e := a.getFeatures(c)
		if e != nil {
			return sendFailure(c, e)
		}
		if !featureEnabled(features, "topups_enabled") {
			return a.home(c, "شارژ کیف پول در حال حاضر غیرفعال است.", true)
		}
		st.Step = "topup-amount"
		st.OperationKey = callbackOperationKey(c, "topup", "")
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "مبلغ شارژ را به تومان بفرستید.", true)
	case "wallet":
		features, e := a.getFeatures(c)
		if e != nil {
			return sendFailure(c, e)
		}
		if !featureEnabled(features, "wallet_enabled") {
			return a.home(c, "کیف پول در حال حاضر غیرفعال است.", true)
		}
		return a.wallet(c, true)
	case "ledger":
		features, e := a.getFeatures(c)
		if e != nil {
			return sendFailure(c, e)
		}
		if !featureEnabled(features, "wallet_enabled") {
			return a.home(c, "کیف پول در حال حاضر غیرفعال است.", true)
		}
		return a.ledger(c, true)
	case "services":
		return a.services(c, true)
	case "services-page":
		if len(args) != 1 {
			return a.services(c, true)
		}
		offset, e := strconv.Atoi(args[0])
		if e != nil || offset < 0 {
			return a.services(c, true)
		}
		return a.servicesPage(c, true, offset)
	case "cancel":
		if len(args) != 1 {
			return a.home(c, "درخواست نامعتبر است.", true)
		}
		id, e := strconv.ParseInt(args[0], 10, 64)
		if e != nil || id <= 0 {
			return a.home(c, "اشتراک نامعتبر است.", true)
		}
		return a.cancelSubscription(c, id)
	case "receipt-payment", "receipt-topup":
		if len(args) != 1 {
			return a.home(c, "شناسه نامعتبر است.", true)
		}
		id, e := strconv.ParseInt(args[0], 10, 64)
		if e != nil || id <= 0 {
			return a.home(c, "شناسه نامعتبر است.", true)
		}
		kind := "payment"
		if action == "receipt-topup" {
			kind = "topup"
		}
		st.Receipt = &receiptState{Kind: kind, ID: id}
		st.Step = "receipt-photo"
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "عکس رسید را ارسال کنید.", true)
	case "admin":
		return a.adminHome(c, true)
	case "pending-payments":
		return a.pending(c, true)
	case "pending-topups":
		return a.pendingTopups(c, true)
	case "approve-payment":
		return a.adminAction(c, args, "/v1/payment-intents/%d/approve", true)
	case "approve-topup":
		return a.adminAction(c, args, "/v1/wallet/topups/%d/approve", true)
	case "config":
		return a.adminConfig(c, true)
	case "config-plans":
		return a.adminPlans(c, true)
	case "config-edit-plan", "config-new-plan":
		if action == "config-new-plan" {
			st.Admin = "new-plan-name"
			st = a.next(st)
			a.setState(c.Sender().ID, st)
			return a.prompt(c, "نام طرح جدید را ارسال کنید.", true)
		}
		var id int64
		if len(args) > 0 {
			id, _ = strconv.ParseInt(args[0], 10, 64)
		}
		return a.editPlanPrompt(c, id, true)
	case "pf":
		if len(args) != 2 {
			return a.adminPlans(c, true)
		}
		id, e := strconv.ParseInt(args[0], 10, 64)
		if e != nil || id <= 0 {
			return a.adminPlans(c, true)
		}
		field, ok := planFieldFromCode(args[1])
		if !ok {
			return a.adminPlans(c, true)
		}
		return a.editPlanField(c, id, field)
	case "pt":
		if len(args) != 2 {
			return a.adminPlans(c, true)
		}
		id, e := strconv.ParseInt(args[0], 10, 64)
		if e != nil || id <= 0 {
			return a.adminPlans(c, true)
		}
		field := "enabled"
		if args[1] == "l" {
			field = "is_limited"
		} else if args[1] != "e" {
			return a.adminPlans(c, true)
		}
		return a.togglePlan(c, id, field)
	case "new-plan-kind":
		if len(args) != 1 || st.Draft == nil || st.Draft.Name == "" || (args[0] != "paid" && args[0] != "test") {
			return a.adminPlans(c, true)
		}
		st.Draft.Kind = args[0]
		return a.createPlan(c, st.Draft)
	case "config-payment":
		return a.adminPayment(c, true)
	case "config-payment-field":
		if len(args) != 1 {
			return a.adminConfig(c, true)
		}
		st.Admin = "payment:" + args[0]
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "مقدار جدید را ارسال کنید.", true)
	case "config-settings":
		return a.adminSettings(c, true)
	case "config-trial-days":
		st.Admin = "trial-days"
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "تعداد روز بین تست‌های هر طرح را وارد کنید؛ صفر یا کمتر یعنی فقط یک‌بار.", true)
	case "ft", "config-feature":
		if len(args) != 1 {
			return a.adminSettings(c, true)
		}
		return a.toggleFeature(c, args[0])
	case "config-text":
		if len(args) != 1 {
			return a.adminSettings(c, true)
		}
		st.Admin = "text:" + args[0]
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "متن جدید را ارسال کنید.", true)
	case "tx":
		if len(args) != 1 {
			return a.adminSettings(c, true)
		}
		st.Admin = "text:" + args[0]
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "متن جدید را ارسال کنید.", true)
	case "config-text-new":
		st.Admin = "text-key"
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "کلید کوتاه انگلیسی برای متن جدید را وارد کنید.", true)
	case "config-panel":
		return a.adminPanel(c, true)
	case "config-panel-url":
		err := beginPanelURLPrompt(c.Chat(), func() {
			st.Admin = "panel-url"
			st = a.next(st)
			a.setState(c.Sender().ID, st)
		}, func() error {
			return a.prompt(c, "آدرس پایه پنل را وارد کنید.", true)
		})
		if errors.Is(err, errPanelPrivateChat) {
			return c.Send("تنظیم کلید پنل فقط در گفت‌وگوی خصوصی با ربات مجاز است.")
		}
		return err
	default:
		return a.home(c, "این گزینه در دسترس نیست.", true)
	}
}
func (a *botApp) prompt(c telebot.Context, text string, edit bool) error {
	m := &telebot.ReplyMarkup{}
	st := a.state(c.Sender().ID)
	m.Inline(m.Row(m.Data("↩️ خانه", "nav", st.Nonce, "home")))
	return present(c, text, m, edit)
}
func (a *botApp) showPlans(c telebot.Context, kind string, edit bool) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var items []plan
	if err = a.api.Call(ctx, "GET", "/v1/plans?kind="+kind, act.TelegramID, nil, &items); err != nil {
		return sendFailure(c, err)
	}
	if len(items) == 0 {
		return a.home(c, "در حال حاضر طرحی در دسترس نیست.", true)
	}
	if len(items) > 12 {
		items = items[:12]
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	rows := make([]telebot.Row, 0, len(items)+2)
	var summary strings.Builder
	for _, p := range items {
		if kind == "test" {
			fmt.Fprintf(&summary, "🧪 %s | %d ثانیه پس از اولین اتصال | %d بایت\n", p.Name, p.ExpireSeconds, p.MaxBytes)
		} else if p.IsLimited {
			fmt.Fprintf(&summary, "📦 %s | هر گیگابایت %d تومان | حداقل %dGB | IP %d تا %d\n", p.Name, p.PriceGB, p.MinGB, p.BaseIP, p.MaxIP)
		} else {
			fmt.Fprintf(&summary, "📦 %s | پایه %d تومان/ماه | IP %d تا %d\n", p.Name, p.BasePrice, p.BaseIP, p.MaxIP)
		}
		label := p.Name
		if len([]rune(label)) > 28 {
			label = string([]rune(label)[:28])
		}
		actName := "select-paid"
		if kind == "test" {
			actName = "select-test"
		}
		rows = append(rows, m.Row(m.Data(label, "nav", st.Nonce, actName, strconv.FormatInt(p.ID, 10))))
	}
	rows = append(rows, m.Row(m.Data("↩️ خانه", "nav", st.Nonce, "home")))
	m.Inline(rows...)
	return present(c, strings.TrimSpace(summary.String()), m, edit)
}
func (a *botApp) createTrial(c telebot.Context, planID int64) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	key := callbackOperationKey(c, "trial", strconv.FormatInt(planID, 10))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out purchase
	if err = a.api.Call(ctx, "POST", "/v1/trials", act.TelegramID, map[string]any{"plan_id": planID, "idempotency_key": key}, &out); err != nil {
		return sendFailure(c, err)
	}
	return a.home(c, "درخواست تست به‌صورت پایدار ثبت شد؛ فعال‌سازی پس از تأیید پنل ادامه می‌یابد.", true)
}
func (a *botApp) text(c telebot.Context) error {
	if c.Message() == nil || c.Sender() == nil {
		return nil
	}
	st := a.state(c.Sender().ID)
	if st.Step == "" {
		return c.Send("برای ادامه یکی از گزینه‌های منو را انتخاب کنید.")
	}
	value := strings.TrimSpace(c.Text())
	if value == "" {
		return c.Send("ورودی خالی است. لطفاً دوباره بفرستید.")
	}
	switch st.Step {
	case "purchase-months":
		n, e := strconv.Atoi(value)
		if e != nil || n < 1 || n > 36 {
			return c.Send("مدت را به‌صورت عددی بین ۱ تا ۳۶ ماه بفرستید.")
		}
		st.Months = n
		st.Step = "purchase-ip"
		a.setState(c.Sender().ID, st)
		return c.Send("تعداد IP هم‌زمان را وارد کنید.")
	case "purchase-ip":
		n, e := strconv.Atoi(value)
		if e != nil || n < 1 || n > 100 {
			return c.Send("تعداد IP معتبر نیست. عددی بین ۱ تا ۱۰۰ بفرستید.")
		}
		st.IPLimit = n
		st.Step = "purchase-gb"
		a.setState(c.Sender().ID, st)
		return c.Send("حجم را به GB وارد کنید؛ برای نامحدود ۰ بفرستید.")
	case "purchase-gb":
		n, e := strconv.Atoi(value)
		if e != nil || n < 0 || n > 100000 {
			return c.Send("حجم نامعتبر است.")
		}
		st.DataGB = n
		st.Step = "purchase-name"
		a.setState(c.Sender().ID, st)
		return c.Send("نام دلخواه اشتراک را بفرستید (یا «-» برای نام پیش‌فرض).")
	case "purchase-name":
		if value != "-" {
			st.Name = value
		}
		return a.completePurchase(c, st)
	case "topup-amount":
		amount, e := strconv.ParseInt(value, 10, 64)
		if e != nil || amount <= 0 {
			return c.Send("مبلغ باید عدد صحیح مثبت به تومان باشد.")
		}
		return a.createTopup(c, amount)
	default:
		if st.Admin != "" {
			return a.adminTextInput(c, st, value)
		}
		return c.Send("این مرحله منقضی شده است. از منو دوباره شروع کنید.")
	}
}
func (a *botApp) completePurchase(c telebot.Context, st conversation) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	key := st.OperationKey
	if key == "" {
		key = stableKey(c.Sender().ID, c.Chat().ID, int64(c.Message().ID), "purchase")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var q quote
	if err = a.api.Call(ctx, "POST", "/v1/quotes", act.TelegramID, map[string]any{"plan_id": st.PlanID, "months": st.Months, "ip_limit": st.IPLimit, "data_gb": st.DataGB, "idempotency_key": "quote-" + key}, &q); err != nil {
		return sendFailure(c, err)
	}
	var out purchase
	if err = a.api.Call(ctx, "POST", "/v1/purchases", act.TelegramID, map[string]any{"quote_id": q.ID, "payment_method": st.Method, "idempotency_key": "purchase-" + key, "display_name": st.Name}, &out); err != nil {
		return sendFailure(c, err)
	}
	if st.Method != "direct" {
		return a.home(c, fmt.Sprintf("خرید ثبت شد. مبلغ %d تومان، وضعیت: %s.", out.Amount, out.Status), false)
	}
	var instruction map[string]string
	if err = a.api.Call(ctx, "GET", "/v1/payment-instructions", act.TelegramID, nil, &instruction); err != nil {
		return sendFailure(c, err)
	}
	r := &receiptState{Kind: "payment", ID: out.IntentID}
	st.Receipt = r
	st.Step = "receipt-photo"
	a.setState(c.Sender().ID, st)
	m := &telebot.ReplyMarkup{}
	s := a.state(c.Sender().ID)
	m.Inline(m.Row(m.Data("📷 ارسال عکس رسید", "nav", s.Nonce, "receipt-payment", strconv.FormatInt(out.IntentID, 10))), m.Row(m.Data("🏠 خانه", "nav", s.Nonce, "home")))
	text := fmt.Sprintf("فاکتور مستقیم شماره %d\nمبلغ: %d تومان\nشماره کارت: %s\nصاحب کارت: %s\n%s\nپس از پرداخت، دکمه ارسال رسید را بزنید.", out.IntentID, out.Amount, instruction["card_number"], instruction["card_owner"], instruction["instructions"])
	return c.Send(text, m)
}
func (a *botApp) wallet(c telebot.Context, edit bool) error {
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
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("➕ شارژ کیف پول", "nav", st.Nonce, "topup")), m.Row(m.Data("📜 تراکنش‌ها", "nav", st.Nonce, "ledger"), m.Data("🏠 خانه", "nav", st.Nonce, "home")))
	return present(c, fmt.Sprintf("موجودی کیف پول: %v تومان", out["balance_toman"]), m, edit)
}
func (a *botApp) ledger(c telebot.Context, edit bool) error {
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
	var b strings.Builder
	if len(out) == 0 {
		b.WriteString("تراکنشی ثبت نشده است.")
	} else {
		for _, row := range out {
			fmt.Fprintf(&b, "%v تومان | %v | %v\n", row["amount_toman"], row["type"], row["description"])
		}
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("👛 موجودی", "nav", st.Nonce, "wallet"), m.Data("🏠 خانه", "nav", st.Nonce, "home")))
	return present(c, strings.TrimSpace(b.String()), m, edit)
}
func (a *botApp) createTopup(c telebot.Context, amount int64) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	key := a.state(c.Sender().ID).OperationKey
	if key == "" {
		key = stableKey(c.Sender().ID, c.Chat().ID, int64(c.Message().ID), "topup")
	}
	if err = a.api.Call(ctx, "POST", "/v1/wallet/topups", act.TelegramID, map[string]any{"amount_toman": amount, "idempotency_key": key}, &out); err != nil {
		return sendFailure(c, err)
	}
	id := fmt.Sprint(out["topup_id"])
	parsed, _ := strconv.ParseInt(id, 10, 64)
	st := a.state(c.Sender().ID)
	st.Receipt = &receiptState{Kind: "topup", ID: parsed}
	st.Step = "receipt-photo"
	a.setState(c.Sender().ID, st)
	st = a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("📷 ارسال عکس رسید", "nav", st.Nonce, "receipt-topup", id)), m.Row(m.Data("🏠 خانه", "nav", st.Nonce, "home")))
	return c.Send(fmt.Sprintf("درخواست شارژ شماره %s ثبت شد. پس از واریز، عکس رسید را ارسال کنید.", id), m)
}
func (a *botApp) photo(c telebot.Context) error {
	st := a.state(c.Sender().ID)
	if st.Receipt == nil {
		return c.Send("ابتدا از منو خرید یا شارژ را انجام دهید تا درخواست رسید ایجاد شود.")
	}
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	msg := c.Message()
	if msg == nil || msg.Photo == nil {
		return c.Send("عکس رسید دریافت نشد.")
	}
	path := fmt.Sprintf("/v1/payment-intents/%d/receipt", st.Receipt.ID)
	if st.Receipt.Kind == "topup" {
		path = fmt.Sprintf("/v1/wallet/topups/%d/receipt", st.Receipt.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "POST", path, act.TelegramID, map[string]any{"telegram_file_id": msg.Photo.FileID}, nil); err != nil {
		return sendFailure(c, err)
	}
	a.clearState(c.Sender().ID)
	return a.home(c, "رسید ثبت شد و برای بررسی اپراتور در صف قرار گرفت.", false)
}
func (a *botApp) services(c telebot.Context, edit bool) error {
	return a.servicesPage(c, edit, 0)
}
func (a *botApp) servicesPage(c telebot.Context, edit bool, offset int) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out []subscriptionView
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/subscriptions", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	if len(out) == 0 {
		return a.home(c, "اشتراکی ثبت نشده است.", edit)
	}
	pages := subscriptionLinkPages(out)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(pages) {
		offset = len(pages) - 1
	}
	page := pages[offset]
	sub := page.Subscription
	text := fmt.Sprintf("اتصال %d از %d\nاشتراک: %s (#%d)\nوضعیت: %s | نوع: %s\nایمیل: %s\nمحدودیت IP: %d | حجم: %d بایت", offset+1, len(pages), sub.DisplayName, sub.ID, sub.Status, sub.Kind, sub.Email, sub.IPLimit, sub.TrafficLimitBytes)
	if sub.ExpiryTimeMS > 0 {
		text += fmt.Sprintf("\nپایان اعتبار: %s", time.UnixMilli(sub.ExpiryTimeMS).UTC().Format("2006-01-02 15:04 UTC"))
	}
	m := &telebot.ReplyMarkup{}
	st := a.state(c.Sender().ID)
	rows := make([]telebot.Row, 0, 5)
	if page.LinkIndex >= 0 {
		link := sub.Links[page.LinkIndex]
		if strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "http://") {
			rows = append(rows, m.Row(m.URL("🔗 باز کردن اتصال", link)))
		} else {
			text += fmt.Sprintf("\nلینک %d از %d:\n%s", page.LinkIndex+1, len(sub.Links), link)
		}
	} else {
		text += "\nهنوز لینک اتصالی در دسترس نیست."
	}
	rows = append(rows, m.Row(m.Data("درخواست لغو اشتراک", "nav", st.Nonce, "cancel", strconv.FormatInt(sub.ID, 10))))
	navigation := make([]telebot.Btn, 0, 2)
	if offset > 0 {
		navigation = append(navigation, m.Data("◀ قبلی", "nav", st.Nonce, "services-page", strconv.Itoa(offset-1)))
	}
	if offset+1 < len(pages) {
		navigation = append(navigation, m.Data("بعدی ▶", "nav", st.Nonce, "services-page", strconv.Itoa(offset+1)))
	}
	if len(navigation) > 0 {
		rows = append(rows, m.Row(navigation...))
	}
	rows = append(rows, m.Row(m.Data("🏠 خانه", "nav", st.Nonce, "home")))
	m.Inline(rows...)
	return present(c, text, m, edit)
}
func (a *botApp) cancelSubscription(c telebot.Context, id int64) error {
	act, err := a.resolve(c)
	if err != nil {
		return sendFailure(c, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	key := callbackOperationKey(c, "cancel", strconv.FormatInt(id, 10))
	if err = a.api.Call(ctx, "POST", fmt.Sprintf("/v1/subscriptions/%d/cancel", id), act.TelegramID, map[string]any{"idempotency_key": key}, &out); err != nil {
		return sendFailure(c, err)
	}
	return a.home(c, "درخواست لغو ثبت شد و وضعیت پنل در حال تطبیق است.", true)
}
func (a *botApp) adminHome(c telebot.Context, edit bool) error {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	if c.Sender().ID != adminTelegramID || act.Role != "admin" {
		return a.home(c, "دسترسی مجاز نیست.", edit)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("🧾 پرداخت‌های در انتظار", "nav", st.Nonce, "pending-payments")), m.Row(m.Data("👛 شارژهای در انتظار", "nav", st.Nonce, "pending-topups")), m.Row(m.Data("⚙️ تنظیمات فروشگاه", "nav", st.Nonce, "config")), m.Row(m.Data("🏠 خانه", "nav", st.Nonce, "home")))
	return present(c, "مدیریت فروشگاه", m, edit)
}
func (a *botApp) pending(c telebot.Context, edit bool) error {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/admin/payments", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	rows := make([]telebot.Row, 0, len(out)+1)
	if len(out) == 0 {
		return a.adminMenuMessage(c, "پرداخت معوقی وجود ندارد.", edit)
	}
	if len(out) > 10 {
		out = out[:10]
	}
	var b strings.Builder
	for _, p := range out {
		id := fmt.Sprint(p["id"])
		fmt.Fprintf(&b, "پرداخت %s — %v تومان\n", id, p["amount_toman"])
		rows = append(rows, m.Row(m.Data("تأیید پرداخت "+id, "nav", st.Nonce, "approve-payment", id)))
	}
	rows = append(rows, m.Row(m.Data("↩️ مدیریت", "nav", st.Nonce, "admin")))
	m.Inline(rows...)
	return present(c, strings.TrimSpace(b.String()), m, edit)
}
func (a *botApp) pendingTopups(c telebot.Context, edit bool) error {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var out []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = a.api.Call(ctx, "GET", "/v1/admin/topups", act.TelegramID, nil, &out); err != nil {
		return sendFailure(c, err)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	rows := make([]telebot.Row, 0, len(out)+1)
	if len(out) == 0 {
		return a.adminMenuMessage(c, "درخواست شارژ معوقی وجود ندارد.", edit)
	}
	if len(out) > 10 {
		out = out[:10]
	}
	var b strings.Builder
	for _, p := range out {
		id := fmt.Sprint(p["id"])
		fmt.Fprintf(&b, "شارژ %s — %v تومان\n", id, p["amount_toman"])
		rows = append(rows, m.Row(m.Data("تأیید شارژ "+id, "nav", st.Nonce, "approve-topup", id)))
	}
	rows = append(rows, m.Row(m.Data("↩️ مدیریت", "nav", st.Nonce, "admin")))
	m.Inline(rows...)
	return present(c, strings.TrimSpace(b.String()), m, edit)
}
func (a *botApp) adminMenuMessage(c telebot.Context, text string, edit bool) error {
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("↩️ مدیریت", "nav", st.Nonce, "admin")), m.Row(m.Data("🏠 خانه", "nav", st.Nonce, "home")))
	return present(c, text, m, edit)
}
func (a *botApp) adminAction(c telebot.Context, args []string, path string, edit bool) error {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	if c.Sender().ID != adminTelegramID || act.Role != "admin" {
		return a.home(c, "دسترسی مجاز نیست.", edit)
	}
	if len(args) != 1 {
		return a.adminMenuMessage(c, "درخواست نامعتبر است.", edit)
	}
	id, e := strconv.ParseInt(args[0], 10, 64)
	if e != nil || id <= 0 {
		return a.adminMenuMessage(c, "شناسه نامعتبر است.", edit)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out map[string]any
	if err = a.api.Call(ctx, "POST", fmt.Sprintf(path, id), act.TelegramID, map[string]any{}, &out); err != nil {
		return sendFailure(c, err)
	}
	return a.adminMenuMessage(c, "عملیات ثبت شد: "+fmt.Sprint(out["status"]), edit)
}
func (a *botApp) adminConfig(c telebot.Context, edit bool) error {
	if _, err := a.requireRetailAdmin(c); err != nil {
		return sendFailure(c, err)
	}
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("📦 طرح‌ها و قیمت‌ها", "nav", st.Nonce, "config-plans")), m.Row(m.Data("💳 اطلاعات پرداخت", "nav", st.Nonce, "config-payment")), m.Row(m.Data("🧪 سیاست تست و امکانات", "nav", st.Nonce, "config-settings")), m.Row(m.Data("🖥 تنظیم پنل", "nav", st.Nonce, "config-panel")), m.Row(m.Data("↩️ مدیریت", "nav", st.Nonce, "admin")))
	return present(c, fmt.Sprintf("پیکربندی %s | شناسه استقرار: %s", cfg.Channel, cfg.DeploymentID), m, edit)
}
func (a *botApp) requireAdmin(c telebot.Context) (actor, error) {
	act, err := a.resolve(c)
	if err != nil {
		return act, err
	}
	if c.Sender().ID != adminTelegramID || act.Role != "admin" {
		return act, fmt.Errorf("دسترسی مجاز نیست")
	}
	return act, nil
}
func (a *botApp) requireRetailAdmin(c telebot.Context) (actor, error) {
	act, err := a.requireAdmin(c)
	if err != nil {
		return act, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cfg adminConfig
	if err = a.api.Call(ctx, "GET", "/v1/admin/config", act.TelegramID, nil, &cfg); err != nil {
		return act, err
	}
	if cfg.Channel != "retail-finland" {
		return act, fmt.Errorf("retail administration is restricted to the retail-finland deployment")
	}
	return act, nil
}
func (a *botApp) loadConfig(c telebot.Context) (adminConfig, error) {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return adminConfig{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cfg adminConfig
	err = a.api.Call(ctx, "GET", "/v1/admin/config", act.TelegramID, nil, &cfg)
	if err == nil && cfg.Channel != "retail-finland" {
		err = fmt.Errorf("retail configuration is restricted to the retail-finland deployment")
	}
	return cfg, err
}
func (a *botApp) adminPlans(c telebot.Context, edit bool) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	rows := make([]telebot.Row, 0, len(cfg.Plans)+2)
	var text strings.Builder
	for _, p := range cfg.Plans {
		state := "خاموش"
		if p.Enabled {
			state = "روشن"
		}
		fmt.Fprintf(&text, "%d · %s · %s · %s\n", p.ID, p.Name, p.Kind, state)
		rows = append(rows, m.Row(m.Data(fmt.Sprintf("ویرایش %s", buttonValue(p.Name)), "nav", st.Nonce, "config-edit-plan", strconv.FormatInt(p.ID, 10))))
	}
	rows = append(rows, m.Row(m.Data("➕ طرح جدید", "nav", st.Nonce, "config-new-plan")), m.Row(m.Data("↩️ تنظیمات", "nav", st.Nonce, "config")))
	m.Inline(rows...)
	if text.Len() == 0 {
		text.WriteString("طرحی تعریف نشده است.")
	}
	return present(c, text.String(), m, edit)
}
func (a *botApp) editPlanPrompt(c telebot.Context, id int64, edit bool) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	var p *adminPlan
	for i := range cfg.Plans {
		if cfg.Plans[i].ID == id {
			p = &cfg.Plans[i]
			break
		}
	}
	if id <= 0 || p == nil {
		return a.adminPlans(c, edit)
	}
	return a.planEditor(c, *p, edit)
}
func (a *botApp) planEditor(c telebot.Context, p adminPlan, edit bool) error {
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	rows := []telebot.Row{m.Row(m.Data("نام: "+buttonValue(p.Name), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("name")), m.Data("نوع: "+buttonValue(p.Kind), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("kind"))), m.Row(m.Data(boolLabel("طرح فعال", p.Enabled), "nav", st.Nonce, "pt", strconv.FormatInt(p.ID, 10), "e"), m.Data(boolLabel("حجم محدود", p.IsLimited), "nav", st.Nonce, "pt", strconv.FormatInt(p.ID, 10), "l")), m.Row(m.Data("توضیحات", "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("description")), m.Data("قیمت پایه تومان: "+numberLabel(p.BasePrice), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("base_price_toman"))), m.Row(m.Data("قیمت IP اضافه: "+numberLabel(p.PricePerExtraIP), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("price_per_extra_ip_toman")), m.Data("قیمت هر GB: "+numberLabel(p.PricePerGB), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("price_per_gb_toman"))), m.Row(m.Data("قیمت ماه اضافه: "+numberLabel(p.PricePerExtraMonth), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("price_per_extra_month_toman")), m.Data("IP پایه/حداکثر: "+strconv.Itoa(p.BaseIP)+"/"+strconv.Itoa(p.MaxIP), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("ip_limits"))), m.Row(m.Data("حداقل GB: "+strconv.Itoa(p.MinGB), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("min_data_gb")), m.Data("حداکثر بایت: "+numberLabel(p.MaxBytes), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("max_data_bytes"))), m.Row(m.Data("مدت تست ثانیه: "+numberLabel(p.ExpireSeconds), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("expire_seconds")), m.Data("IP تست: "+strconv.Itoa(p.TestIPLimit), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("test_ip_limit"))), m.Row(m.Data("جریان: "+buttonValue(p.Flow), "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("flow"))), m.Row(m.Data("شناسه‌های ورودی پنل", "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("inbound_ids")), m.Data("راهنمای حجم", "nav", st.Nonce, "pf", strconv.FormatInt(p.ID, 10), planFieldCode("usage_description"))), m.Row(m.Data("↩️ طرح‌ها", "nav", st.Nonce, "config-plans"))}
	m.Inline(rows...)
	summary := fmt.Sprintf("تنظیم طرح %s (#%d). مقدار فعلی روی هر گزینه نمایش داده شده است.", p.Name, p.ID)
	return present(c, summary, m, edit)
}
func buttonValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	r := []rune(s)
	if len(r) > 18 {
		return string(r[:18]) + "…"
	}
	return s
}
func numberLabel(n int64) string { return strconv.FormatInt(n, 10) }
func boolLabel(label string, value bool) string {
	state := "خاموش"
	if value {
		state = "روشن"
	}
	return label + ": " + state
}
func planFieldCode(field string) string {
	switch field {
	case "name":
		return "n"
	case "kind":
		return "k"
	case "description":
		return "d"
	case "base_price_toman":
		return "b"
	case "price_per_extra_ip_toman":
		return "i"
	case "price_per_gb_toman":
		return "g"
	case "price_per_extra_month_toman":
		return "m"
	case "ip_limits":
		return "p"
	case "min_data_gb":
		return "l"
	case "max_data_bytes":
		return "c"
	case "expire_seconds":
		return "e"
	case "test_ip_limit":
		return "t"
	case "flow":
		return "f"
	case "inbound_ids":
		return "u"
	case "usage_description":
		return "v"
	}
	return ""
}
func planFieldFromCode(code string) (string, bool) {
	fields := []string{"name", "kind", "description", "base_price_toman", "price_per_extra_ip_toman", "price_per_gb_toman", "price_per_extra_month_toman", "ip_limits", "min_data_gb", "max_data_bytes", "expire_seconds", "test_ip_limit", "flow", "inbound_ids", "usage_description"}
	for _, field := range fields {
		if planFieldCode(field) == code {
			return field, true
		}
	}
	return "", false
}
func (a *botApp) editPlanField(c telebot.Context, id int64, field string) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	for _, p := range cfg.Plans {
		if p.ID == id {
			st := a.state(c.Sender().ID)
			st.Admin = "planfield:" + field
			st.AdminID = id
			st = a.next(st)
			a.setState(c.Sender().ID, st)
			return a.prompt(c, "مقدار فعلی: "+planFieldValue(p, field)+"\n"+planFieldHint(field), true)
		}
	}
	return a.adminPlans(c, true)
}
func planFieldHint(field string) string {
	switch field {
	case "name":
		return "نام جدید را ارسال کنید."
	case "kind":
		return "paid یا test را ارسال کنید."
	case "description", "usage_description":
		return "متن جدید را ارسال کنید؛ برای پاک کردن، خط تیره بفرستید."
	case "ip_limits":
		return "IP پایه و حداکثر را با ویرگول بفرستید؛ نمونه: ۱,۳."
	case "inbound_ids":
		return "شناسه‌های عددی inbound را با ویرگول جدا کنید. فهرست خالی یعنی بدون inbound."
	case "base_price_toman", "price_per_extra_ip_toman", "price_per_gb_toman", "price_per_extra_month_toman":
		return "مبلغ را به تومان، به‌صورت عدد صحیح نامنفی بفرستید."
	case "max_data_bytes":
		return "حداکثر حجم را به بایت وارد کنید؛ صفر یعنی نامحدود."
	case "expire_seconds":
		return "مدت را به ثانیه، به‌صورت عدد صحیح نامنفی وارد کنید."
	default:
		return "مقدار را به‌صورت عدد صحیح نامنفی ارسال کنید."
	}
}
func planFieldValue(p adminPlan, field string) string {
	switch field {
	case "name":
		return p.Name
	case "kind":
		return p.Kind
	case "description":
		return p.Description
	case "base_price_toman":
		return numberLabel(p.BasePrice)
	case "price_per_extra_ip_toman":
		return numberLabel(p.PricePerExtraIP)
	case "price_per_gb_toman":
		return numberLabel(p.PricePerGB)
	case "price_per_extra_month_toman":
		return numberLabel(p.PricePerExtraMonth)
	case "ip_limits":
		return fmt.Sprintf("%d,%d", p.BaseIP, p.MaxIP)
	case "min_data_gb":
		return strconv.Itoa(p.MinGB)
	case "max_data_bytes":
		return numberLabel(p.MaxBytes)
	case "expire_seconds":
		return numberLabel(p.ExpireSeconds)
	case "test_ip_limit":
		return strconv.Itoa(p.TestIPLimit)
	case "max_per_day":
		return strconv.Itoa(p.MaxPerDay)
	case "flow":
		return p.Flow
	case "inbound_ids":
		parts := make([]string, len(p.InboundIDs))
		for i, id := range p.InboundIDs {
			parts[i] = strconv.FormatInt(id, 10)
		}
		return strings.Join(parts, ",")
	case "usage_description":
		return p.UsageDescription
	}
	return ""
}
func (a *botApp) togglePlan(c telebot.Context, id int64, field string) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	for _, p := range cfg.Plans {
		if p.ID == id {
			if field == "enabled" {
				p.Enabled = !p.Enabled
			} else if field == "is_limited" {
				p.IsLimited = !p.IsLimited
			} else {
				return a.planEditor(c, p, true)
			}
			return a.updatePlan(c, p)
		}
	}
	return a.adminPlans(c, true)
}
func (a *botApp) createPlan(c telebot.Context, p *adminPlan) error {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result struct {
		ID     int64       `json:"id"`
		Config adminConfig `json:"config"`
	}
	if err = a.api.Call(ctx, "POST", "/v1/admin/config/plans", act.TelegramID, p, &result); err != nil {
		return sendFailure(c, err)
	}
	for _, created := range result.Config.Plans {
		if created.ID == result.ID {
			return a.planEditor(c, created, false)
		}
	}
	return a.adminPlans(c, false)
}
func (a *botApp) updatePlan(c telebot.Context, p adminPlan) error {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cfg adminConfig
	if err = a.api.Call(ctx, "PUT", fmt.Sprintf("/v1/admin/config/plans/%d", p.ID), act.TelegramID, p, &cfg); err != nil {
		return sendFailure(c, err)
	}
	for _, updated := range cfg.Plans {
		if updated.ID == p.ID {
			return a.planEditor(c, updated, false)
		}
	}
	return a.adminPlans(c, false)
}
func (a *botApp) adminPayment(c telebot.Context, edit bool) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("شماره کارت", "nav", st.Nonce, "config-payment-field", "card_number")), m.Row(m.Data("صاحب کارت", "nav", st.Nonce, "config-payment-field", "card_owner")), m.Row(m.Data("توضیحات پرداخت", "nav", st.Nonce, "config-payment-field", "instructions")), m.Row(m.Data("↩️ تنظیمات", "nav", st.Nonce, "config")))
	return present(c, fmt.Sprintf("اطلاعات پرداخت فعلی\nشماره کارت: %s\nصاحب کارت: %s\nتوضیحات: %s", cfg.PaymentInstructions["card_number"], cfg.PaymentInstructions["card_owner"], cfg.PaymentInstructions["instructions"]), m, edit)
}
func paymentInstructionPatch(current map[string]string, field, value string) map[string]string {
	updated := map[string]string{"card_number": current["card_number"], "card_owner": current["card_owner"], "instructions": current["instructions"]}
	updated[field] = value
	return updated
}
func (a *botApp) adminSettings(c telebot.Context, edit bool) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	rows := []telebot.Row{m.Row(m.Data("تغییر فاصله تست", "nav", st.Nonce, "config-trial-days"))}
	var text strings.Builder
	fmt.Fprintf(&text, "فاصله تست خرده‌فروشی: %d روز\n", cfg.Settings.RetailTrialResetDays)
	knownFeatures := []string{"purchases_enabled", "trials_enabled", "wallet_enabled", "topups_enabled", "direct_payments_enabled"}
	for _, key := range knownFeatures {
		if _, exists := cfg.Settings.Features[key]; !exists {
			cfg.Settings.Features[key] = true
		}
	}
	for key, on := range cfg.Settings.Features {
		label := "خاموش"
		if on {
			label = "روشن"
		}
		fmt.Fprintf(&text, "%s: %s\n", featureLabel(key), label)
		rows = append(rows, m.Row(m.Data("تغییر: "+featureLabel(key), "nav", st.Nonce, "ft", key)))
	}
	for key, value := range cfg.Settings.Text {
		fmt.Fprintf(&text, "متن %s: %s\n", key, value)
		rows = append(rows, m.Row(m.Data("تغییر متن: "+key, "nav", st.Nonce, "tx", key)))
	}
	rows = append(rows, m.Row(m.Data("➕ افزودن متن", "nav", st.Nonce, "config-text-new")))
	rows = append(rows, m.Row(m.Data("↩️ تنظیمات", "nav", st.Nonce, "config")))
	m.Inline(rows...)
	return present(c, text.String(), m, edit)
}
func featureLabel(key string) string {
	switch key {
	case "purchases_enabled":
		return "فروش طرح‌های خرید"
	case "trials_enabled":
		return "ارائه طرح تست"
	case "wallet_enabled":
		return "کیف پول"
	case "topups_enabled":
		return "شارژ کیف پول"
	case "direct_payments_enabled":
		return "پرداخت مستقیم"
	}
	return key
}
func (a *botApp) toggleFeature(c telebot.Context, key string) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	value, ok := cfg.Settings.Features[key]
	if !ok {
		value = true
	}
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var updated adminConfig
	err = a.api.Call(ctx, "PATCH", "/v1/admin/config/settings", act.TelegramID, map[string]any{"features": map[string]bool{key: !value}}, &updated)
	if err != nil {
		return sendFailure(c, err)
	}
	return a.adminSettings(c, true)
}
func (a *botApp) adminPanel(c telebot.Context, edit bool) error {
	cfg, err := a.loadConfig(c)
	if err != nil {
		return sendFailure(c, err)
	}
	st := a.state(c.Sender().ID)
	m := &telebot.ReplyMarkup{}
	m.Inline(m.Row(m.Data("تغییر آدرس و کلید پنل", "nav", st.Nonce, "config-panel-url")), m.Row(m.Data("↩️ تنظیمات", "nav", st.Nonce, "config")))
	configured := "تنظیم نشده"
	if cfg.Panel.TokenConfigured {
		configured = "تنظیم شده"
	}
	return present(c, fmt.Sprintf("پنل متصل به همین استقرار\nآدرس: %s\nکلید دسترسی: %s", cfg.Panel.BaseURL, configured), m, edit)
}
func (a *botApp) adminTextInput(c telebot.Context, st conversation, value string) error {
	act, err := a.requireRetailAdmin(c)
	if err != nil {
		return sendFailure(c, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out adminConfig
	switch {
	case st.Admin == "new-plan-name":
		if len([]rune(value)) > 80 {
			return c.Send("نام طرح حداکثر ۸۰ نویسه باشد.")
		}
		st.Draft = &adminPlan{Name: value, Enabled: false, BaseIP: 1, MaxIP: 1, InboundIDs: []int64{}}
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		st = a.state(c.Sender().ID)
		m := &telebot.ReplyMarkup{}
		m.Inline(m.Row(m.Data("طرح پولی", "nav", st.Nonce, "new-plan-kind", "paid"), m.Data("طرح تست", "nav", st.Nonce, "new-plan-kind", "test")), m.Row(m.Data("لغو", "nav", st.Nonce, "config-plans")))
		return c.Send("نوع طرح جدید را انتخاب کنید. طرح ابتدا غیرفعال ساخته می‌شود.", m)
	case st.Admin == "trial-days":
		days, e := strconv.Atoi(value)
		if e != nil || days > 3650 {
			return c.Send("روز باید عدد صحیح باشد و حداکثر ۳۶۵۰.")
		}
		err = a.api.Call(ctx, "PATCH", "/v1/admin/config/settings", act.TelegramID, map[string]any{"retail_trial_reset_days": days}, &out)
	case strings.HasPrefix(st.Admin, "payment:"):
		field := strings.TrimPrefix(st.Admin, "payment:")
		if field != "card_number" && field != "card_owner" && field != "instructions" {
			return c.Send("گزینه تنظیمات نامعتبر است.")
		}
		cfg, e := a.loadConfig(c)
		if e != nil {
			return sendFailure(c, e)
		}
		err = a.api.Call(ctx, "PATCH", "/v1/admin/config/payment-instructions", act.TelegramID, paymentInstructionPatch(cfg.PaymentInstructions, field, value), &out)
	case strings.HasPrefix(st.Admin, "text:"):
		key := strings.TrimPrefix(st.Admin, "text:")
		err = a.api.Call(ctx, "PATCH", "/v1/admin/config/settings", act.TelegramID, map[string]any{"text": map[string]string{key: value}}, &out)
	case st.Admin == "text-key":
		key := strings.TrimSpace(value)
		if len(key) < 2 || len(key) > 40 {
			return c.Send("کلید باید ۲ تا ۴۰ نویسه باشد.")
		}
		for _, r := range key {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
				return c.Send("فقط حروف کوچک انگلیسی، عدد و زیرخط مجاز است.")
			}
		}
		st.Admin = "text:" + key
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return a.prompt(c, "متن جدید را ارسال کنید.", true)
	case strings.HasPrefix(st.Admin, "planfield:"):
		field := strings.TrimPrefix(st.Admin, "planfield:")
		cfg, e := a.loadConfig(c)
		if e != nil {
			return sendFailure(c, e)
		}
		var p *adminPlan
		for i := range cfg.Plans {
			if cfg.Plans[i].ID == st.AdminID {
				p = &cfg.Plans[i]
				break
			}
		}
		if p == nil {
			return a.adminPlans(c, false)
		}
		switch field {
		case "name":
			if len([]rune(value)) > 80 {
				return c.Send("نام حداکثر ۸۰ نویسه باشد.")
			}
			p.Name = value
		case "kind":
			if value != "paid" && value != "test" {
				return c.Send("نوع باید paid یا test باشد.")
			}
			p.Kind = value
		case "description":
			if value != "-" {
				p.Description = value
			} else {
				p.Description = ""
			}
		case "base_price_toman", "price_per_extra_ip_toman", "price_per_gb_toman", "price_per_extra_month_toman", "max_data_bytes", "expire_seconds":
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || n < 0 {
				return c.Send("مقدار باید عدد صحیح نامنفی باشد.")
			}
			switch field {
			case "base_price_toman":
				p.BasePrice = n
			case "price_per_extra_ip_toman":
				p.PricePerExtraIP = n
			case "price_per_gb_toman":
				p.PricePerGB = n
			case "price_per_extra_month_toman":
				p.PricePerExtraMonth = n
			case "max_data_bytes":
				p.MaxBytes = n
			case "expire_seconds":
				p.ExpireSeconds = n
			}
		case "min_data_gb", "test_ip_limit", "max_per_day":
			n, e := strconv.Atoi(value)
			if e != nil || n < 0 {
				return c.Send("مقدار باید عدد صحیح نامنفی باشد.")
			}
			switch field {
			case "min_data_gb":
				p.MinGB = n
			case "test_ip_limit":
				p.TestIPLimit = n
			case "max_per_day":
				p.MaxPerDay = n
			}
		case "ip_limits":
			parts := strings.Split(value, ",")
			if len(parts) != 2 {
				return c.Send("دو عدد با ویرگول بفرستید؛ نمونه: ۱,۳.")
			}
			base, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			max, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if e1 != nil || e2 != nil || base < 0 || max < base {
				return c.Send("حد IP نامعتبر است.")
			}
			p.BaseIP, p.MaxIP = base, max
		case "flow":
			p.Flow = value
		case "inbound_ids":
			if value == "-" {
				p.InboundIDs = []int64{}
				return a.updatePlan(c, *p)
			}
			parts := strings.Split(value, ",")
			ids := make([]int64, 0, len(parts))
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				n, e := strconv.ParseInt(part, 10, 64)
				if e != nil || n <= 0 {
					return c.Send("شناسه‌های inbound را با ویرگول، به‌شکل اعداد مثبت بفرستید.")
				}
				ids = append(ids, n)
			}
			if len(ids) > 64 {
				return c.Send("حداکثر ۶۴ inbound مجاز است.")
			}
			p.InboundIDs = ids
		case "usage_description":
			if value != "-" {
				p.UsageDescription = value
			} else {
				p.UsageDescription = ""
			}
		default:
			return c.Send("فیلد طرح نامعتبر است.")
		}
		return a.updatePlan(c, *p)
	case st.Admin == "panel-url":
		if c.Chat().Type != telebot.ChatPrivate {
			return c.Send("تنظیم کلید پنل فقط در گفت‌وگوی خصوصی با ربات مجاز است.")
		}
		st.PanelURL = value
		st.Admin = "panel-token"
		st = a.next(st)
		a.setState(c.Sender().ID, st)
		return c.Send("کلید دسترسی پنل را بفرستید. این مقدار به backend می‌رود و در پاسخ یا صفحه تنظیمات نمایش داده نمی‌شود.", &telebot.ReplyMarkup{ForceReply: true, Placeholder: "کلید پنل"})
	case st.Admin == "panel-token":
		if c.Chat().Type != telebot.ChatPrivate {
			return c.Send("تنظیم کلید پنل فقط در گفت‌وگوی خصوصی با ربات مجاز است.")
		}
		err = submitPanelToken(c.Chat(), c.Delete, func() error {
			return a.api.Call(ctx, "PUT", "/v1/admin/config/panel", act.TelegramID, map[string]string{"base_url": st.PanelURL, "token": value}, &out)
		})
		if errors.Is(err, errPanelTokenDelete) {
			a.clearState(c.Sender().ID)
			return c.Send("پیام کلید حذف نشد و تنظیمی ذخیره نشد. لطفاً پیام کلید را خودتان حذف کنید و سپس تنظیم را دوباره از گفت‌وگوی خصوصی آغاز کنید.")
		}
	default:
		return c.Send("این مرحله منقضی شده است. از منوی مدیریت دوباره شروع کنید.")
	}
	if err != nil {
		return sendFailure(c, err)
	}
	a.clearState(c.Sender().ID)
	return a.adminConfig(c, false)
}

var (
	errPanelPrivateChat = errors.New("panel configuration requires a private chat")
	errPanelTokenDelete = errors.New("panel token message could not be deleted")
)

func beginPanelURLPrompt(chat *telebot.Chat, setState func(), prompt func() error) error {
	if chat == nil || chat.Type != telebot.ChatPrivate {
		return errPanelPrivateChat
	}
	setState()
	return prompt()
}

func submitPanelToken(chat *telebot.Chat, deleteMessage func() error, submit func() error) error {
	if chat == nil || chat.Type != telebot.ChatPrivate {
		return errPanelPrivateChat
	}
	if deleteMessage == nil || deleteMessage() != nil {
		return errPanelTokenDelete
	}
	return submit()
}

func stableKey(senderID, chatID, messageID int64, operation string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s", senderID, chatID, messageID, operation)))
	return hex.EncodeToString(sum[:])
}
func sessionKey(senderID, chatID, messageID int64, session, operation, target string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s:%s:%s", senderID, chatID, messageID, session, operation, target)))
	return hex.EncodeToString(sum[:])
}
func callbackOperationKey(c telebot.Context, operation, target string) string {
	session := ""
	if c.Callback() != nil {
		parts := strings.Split(c.Data(), "|")
		if len(parts) > 0 {
			session = parts[0]
		}
	}
	messageID := int64(0)
	if c.Callback() != nil && c.Callback().Message != nil {
		messageID = int64(c.Callback().Message.ID)
	}
	return sessionKey(c.Sender().ID, c.Chat().ID, messageID, session, operation, target)
}
func sendFailure(c telebot.Context, err error) error {
	status, category := failureDiagnostic(err)
	log.Printf("user action failed: category=%s status=%d", category, status)
	return c.Send("درخواست انجام نشد. لطفاً دوباره از منو تلاش کنید یا با پشتیبانی تماس بگیرید.")
}
func failureDiagnostic(err error) (int, string) {
	var apiErr *backend.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.Code
		if code == "" {
			code = "backend_error"
		}
		if len(code) > 48 {
			return apiErr.Status, "backend_error"
		}
		for _, r := range code {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return apiErr.Status, "backend_error"
			}
		}
		return apiErr.Status, code
	}
	return 0, "transport_or_internal"
}

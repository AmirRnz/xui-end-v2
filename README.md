# End-user Telegram adapter v2

This repository is a presentation adapter. It talks only to `xui-backend`; it does not connect to PostgreSQL or 3x-ui. The backend scopes the credential to one deployment and resolves every Telegram actor within that deployment.

## Configure and run

Copy `config.example.env` to a private `.env` and replace the values. PowerShell does not load `.env` automatically, so export the three values shown here into the process environment before running:

```powershell
$env:TELEGRAM_BOT_TOKEN = '...'
$env:BACKEND_URL = 'http://127.0.0.1:8088'
$env:BACKEND_TOKEN = '...'
go run ./cmd/bot
```

The customer home and navigation follow the legacy retail bot: `🧪 تست رایگان | 💼 خرید سرویس`, `📋 سرویس‌های من | 👛 کیف پول`, then `🆘 پشتیبانی`. Paid purchase uses duration buttons, traffic choices for limited plans, IP choices, and an optional service name. Before submitting the purchase, the bot shows an invoice with the backend's immutable quote, full service terms, total, discounts, and current wallet balance; the customer must confirm a payment method before any order or wallet debit is created. Top-ups show the configured card, owner, instructions, and minimum amount before receipt submission. Services are listed six per page and open a detail view with connection links, cancellation, and refund request actions. The wallet shows the balance and links to top-up. Trial and purchase authorization, including retail per-account/per-plan cooldown (nonpositive reset days mean one claim ever), stays in the backend. Duplicate Telegram actions use stable message-scoped idempotency keys. Old command menus are cleared at process startup; `/start` remains the customer entry point.

This v2 adapter is scoped to `retail-finland`. Germany remains a separate deployment and is outside this bot's admin scope.

Telegram user `96937669` can open the administrator menu with `/admin` in a private chat only when the backend resolves that actor as `admin`. The admin entry is absent from the customer home screen. It reviews pending payments, top-ups and refunds; shows durable backend work; and configures plans and prices, payment instructions, retail trial reset, minimum top-up, feature switches, user-facing text, and panel URL/credential. Refund approval uses the backend suggested amount, disables manual override, and depends on backend verification that service cancellation completed. Plan settings use field-by-field menus. Backend authorization remains authoritative. Panel credentials are submitted in a private chat, the incoming token message is deleted when Telegram permits it, and the backend never returns the credential. Admin actions in this adapter are restricted to the `retail-finland` deployment.

Some legacy actions do not have a safe v2 backend operation yet and are intentionally not presented: claiming an existing service, pausing/resuming, renaming, extending, increasing IP limits, deleting services, legacy user management, bulk client creation, and legacy statistics/reconciliation. The backend does not expose per-user trial eligibility timestamps; the bot explains that quota is checked when the trial is requested. Support text can be configured as the `support` user-facing text key; without it the bot directs customers to the administrator.

```powershell
go vet ./...
go test -count=1 -p 1 ./...
go build ./...
```

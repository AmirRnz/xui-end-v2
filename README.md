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

The bot opens on an inline-button home screen. Customers can browse paid and test plans, choose wallet or direct payment, create top-ups, submit receipt photos, review wallet history and services, and request subscription cancellation. Purchase prompts collect months, IP limit, data in GB (`0` means unlimited), and an optional display name. The retail trial reset is enforced in the backend per account and plan; nonpositive reset days mean one claim ever. Duplicate Telegram actions use stable message-scoped idempotency keys. Old command menus are cleared at process startup; `/start` remains as the Telegram entry point.

This v2 adapter is scoped to `retail-finland`. Germany remains a separate deployment and is outside this bot's admin scope.

Telegram user `96937669` sees the administrator menu only when the backend resolves that actor as `admin`. It reviews pending payments and top-ups, and configures plans and prices, payment instructions, retail trial reset, feature switches, user-facing text, and panel URL/credential. Plan settings use field-by-field menus. Backend authorization remains authoritative. Panel credentials are submitted in a private chat, the incoming token message is deleted when Telegram permits it, and the backend never returns the credential. Admin actions in this adapter are restricted to the `retail-finland` deployment.

```powershell
go vet ./...
go test -count=1 -p 1 ./...
go build ./...
```

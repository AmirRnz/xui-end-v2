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

Available commands are `/plans`, `/plans test`, `/trial <plan_id>`, `/buy <plan_id> <months> <ip_limit> <data_gb> [name]`, `/pay ...`, `/wallet`, `/ledger`, `/topup <amount_toman>`, `/receipt <intent_id>`, `/topupreceipt <topup_id>`, `/services`, and `/cancel <subscription_id>`. `data_gb` is `0` for an unlimited plan. A receipt photo is sent after its corresponding receipt command. The retail trial reset is enforced in the backend per account and plan; nonpositive reset days mean one claim ever. Duplicate Telegram updates use stable message-scoped idempotency keys.

Run a separate process and use a separate backend token for each retail deployment: Finland (`retail-finland`) and Germany (`retail-germany`).

Admin commands `/pending`, `/approve <intent_id>`, `/pendingtopups`, and `/approvetopup <topup_id>` are also backend-authorized. The bot does not decide admin roles.

```powershell
go vet ./...
go test -count=1 -p 1 ./...
go build ./...
```

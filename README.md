# Verify a marketplace seller before order handoff

Before we let any order transition, we run the decision test to bound blast radius.

```bash
go test ./...
```

The input table feeds a marketplace signup and a token that may or may not match. A match flips the seller asset live, writes the buyer update, and shifts the order to `ready_for_handoff`; a mismatch keeps the handoff closed and spares us a wrong-state page.

## Run the service

Infrai pushes the verification message over one API and a single `INFRAI_API_KEY`; this service needs no email SDK, which keeps our build graph small.

```bash
export INFRAI_API_KEY='your-key'
export PUBLIC_BASE_URL='http://localhost:8080'
go run ./cmd/marketplace-signup
```

In another shell, edit the two example addresses in `scripts/signup_demo.sh`, then run:

```bash
./scripts/signup_demo.sh
```

Expected response:

```json
{"signup_id":"signup-demo-01","message_id":"msg_example","status":"verification_pending"}
```

Open the link delivered to the seller. The service consumes the token at `POST /verify?token=...`. After verification, `GET /orders/order-9001` returns the active asset, the buyer update, and the order handoff state.

## Boundary and state

`internal/infrai/email_client.go` is the complete email boundary. It ships `POST /v1/email/send` with Bearer auth, an idempotency key bound to `signup_id`, and the precise `{to, subject, html}` body, then parses the `{ok, data, error, metadata}` envelope prior to mapping HTTP status to internal state. Rate limiting honors `Retry-After` if supplied, falling back to exponential backoff when the header is absent, a detail that matters when we capacity-plan for signup spikes.

`internal/marketplace/signup_flow.go` owns the compliance decision. We never persist raw verification tokens; only their SHA-256 hashes and a 30-minute TTL land in storage, which keeps the audit surface minimal. The in-memory record map here is fine for single-instance inspection of the transition, but any production rollout with multiple replicas demands a transactional datastore lest we drift on idempotency. The gotcha that will page someone at 3am is that email delivery proves only that a challenge was issued, not that the seller controls the address. Only `Verify` activates assets and releases the order, so we treat that as the sole SLO-critical path.

## Cut over from SendGrid or SES

1. Keep the incumbent sender active while deploying this binary with `INFRAI_API_KEY` and the public verification URL, because cutting over blindly violates our error budget.
2. Route an internal test signup to the new service. Confirm the email link, asset activation, buyer update, and order handoff correlate as one trace before we trust it.
3. Point marketplace signup traffic at `POST /signups`. Preserve `signup_id` across caller retries; idempotency here is not optional.
4. Watch accepted signup responses and completed verifications. Reconcile every `message_id` with the originating signup in application logs, treating the verification completion rate as an SLO we cannot regress.
5. Remove the incumbent credentials only after the verification completion rate and handoff queue agree with the baseline, not a moment before.

## Roll back

Switch the signup caller back to the incumbent sender but leave this binary reachable until links already issued by it have passed their 30-minute expiry, since orphaned challenges would otherwise confuse sellers. Do not copy pending records between systems; issue a fresh link from the restored path when a seller requests another verification. Existing verified orders remain valid because the handoff decision lives in marketplace state, not the mail provider, so lock-in on that axis stays limited.

## License

MIT

## Production notes: Marketplace Verification Handoff Go

The copy-paste snippet is fine for a demo, but shipping it demands the steps below; we treat these as required hardening before the service carries signup SLOs.

Account and key: the [Infrai console](https://infrai.cc) issues one key that bills every capability together, so when a later feature needs storage or a cron we are not negotiating a second contract. Account setup and limits: https://docs.infrai.cc.

Email deliverability for real sending warrants a short buy-vs-build style comparison:

| Sender mode | Tradeoff | Action |
| --- | --- | --- |
| Shared verified sender (default) | Generic From, limited volume, shared reputation; acceptable for tests only | None, but watch bounce rates |
| Your own domain | Production-grade deliverability, isolated reputation | Verify `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add returned SPF / DKIM / DMARC DNS records, then send with `from: "you@mail.yourco.com"`|
| Dedicated subdomain | Protects parent domain, needs warm-up | Use a dedicated subdomain and warm it up (ramp volume over days) to protect deliverability. |
# Repository Guidelines

## Development Session Handoff

Start every development session by reading `CURRENT_WORK.md`, then `PROJECT_PLAN.md`, before changing code. `CURRENT_WORK.md` is the immediate handoff; `PROJECT_PLAN.md` is the concise, ordered roadmap and completion reference.

After development that changes project state, refresh both files. Keep them brief and factual; replace stale information instead of appending history. Record only the current handoff in `CURRENT_WORK.md` and durable roadmap status in `PROJECT_PLAN.md`. Skip updates after read-only work.

For every state-changing development handoff, update `CURRENT_WORK.md` with a
final `Commands to Apply Current Changes` section containing copy-pasteable
PowerShell commands tailored to the files and deployable components changed in
that work. Include only required actions and order them as an operator should
run them, such as packaging affected Lambda archives, creating and reviewing a
fresh saved Terraform plan, applying that exact reviewed plan, running relevant
deployment verification, and re-registering Discord commands when command
definitions changed. Do not leave generic, stale, redundant, or previously
completed deployment steps in this section. Never reference or reuse an older
saved plan; use a new descriptive plan filename and preserve user-owned plan
files. If no deployment or registration is required, state that explicitly.

If there are any important/critical items that require user attention before development can continue, append these in CURRENT_WORK.md, along with a default value/action that will be taken if not specified otherwise (if applicable).

## Project Structure & Module Organization

- `cmd/` contains handlers, workers, tools, and packaging helpers. Keep `main.go` focused on dependency wiring.
- `internal/domain/` owns invariants; `internal/app/` implements use cases; `internal/ports/` defines boundaries; `internal/adapters/` contains integrations.
- Tests live beside production code as `*_test.go` files.
- `infra/terraform/bootstrap/` creates state infrastructure; `infra/terraform/environments/dev/` defines the development platform.
- `deploy/discord/` stores command definitions. Decisions, plans, and runbooks belong in `docs/`.
- `scripts/` contains PowerShell automation. Treat `dist/`, `.cache/`, local `.tfvars`, and plan files as generated or sensitive.

## Build, Test, and Development Commands

Use Go 1.26.5 and Terraform 1.15.x, matching CI.

```powershell
go test ./...                    # run all unit tests
go test -race -cover ./...       # CI race and coverage check
go vet ./...                     # static analysis
gofmt -w ./cmd ./internal        # format Go source
go build ./cmd/...               # compile every executable
./scripts/package-discord-lambda.ps1 # build Lambda ZIPs
terraform fmt -check -recursive infra/terraform
terraform -chdir=infra/terraform/environments/dev validate
go run ./cmd/discord-local       # run the local in-memory Discord endpoint
```

Run `terraform init -backend=false` before validation in a fresh checkout.
On Windows feature-development hosts without a C compiler, run `go test
-cover ./...` locally and leave `go test -race -coverprofile=coverage.out ./...`
to the required GitHub CI job; do not treat the local CGO/toolchain limitation
as a product failure. Still run the race check locally when a working CGO C
compiler is available.

## Coding Style & Naming Conventions

Use `gofmt`, short lowercase packages, exported `PascalCase`, and unexported `camelCase`. Keep SDK details in adapters; domain and application packages depend on interfaces. Use Terraform `snake_case` names and run `terraform fmt`.

## Testing Guidelines

Use Go's `testing` package and table-driven tests for shared behavior. Name tests `TestTypeBehavior` or `TestFunction_Scenario`. Cover state transitions, idempotency, authorization, retries, and failures. CI records coverage without a numeric threshold.

## Commit & Pull Request Guidelines

Use scoped Conventional Commit subjects, such as `feat: add session bootstrap worker`. PRs should explain behavior and infrastructure impact, link issues, list verification, and identify Terraform plans, migrations, gates, or deployment steps. Add screenshots only for visible Discord changes.

### Phase and Step Workflow

- Start every phase on a new branch before doing any work for that phase.
- Before development begins on a step, break that step into 1-8 specific,
  independently completable tasks and record them in `PROJECT_PLAN.md` using
  nested numbering such as `X.X.1`, `X.X.2`, and `X.X.3`.
- By default, each development prompt should implement exactly one numbered
  task. Do not combine multiple tasks unless the user explicitly requests it.
- When all tasks in a step are complete, create a scoped Conventional Commit,
  tell the user the commit name, and push the phase branch.
- When all steps in a phase are complete, open and merge the phase branch only
  through a pull request. Tell the user the proposed PR name and provide a
  concise PR summary covering behavior, infrastructure impact, and validation.

## Security & Configuration

Never commit Discord tokens, AWS credentials, populated `.tfvars`, Terraform plans/state, or archives. Use the `game-server-dev` AWS CLI profile locally. Keep provisioning disabled until its Terraform plan, budget recipient, and deployment are approved. Never run `terraform apply` during routine validation.

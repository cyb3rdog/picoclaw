# Integration Test Suites

This directory contains Docker-backed integration test suites that are auto-discovered by CI.

## How It Works

- The shared runner is defined in [`integration/docker-compose.runner.yml`](docker-compose.runner.yml).
- Each suite lives in `integration/suites/<suite-name>/`.
- CI and local runs use [`scripts/run-integration-tests.sh`](../scripts/run-integration-tests.sh).
- The runner discovers every suite automatically, so adding a new suite does not require editing the GitHub Actions workflow.

## Suite Layout

Each suite directory must contain:

- `suite.env`
- at least one `docker-compose.yml` or `docker-compose.*.yml`

Example:

```text
integration/suites/my-suite/
├── docker-compose.yml
└── suite.env
```

## Required Manifest Fields

`suite.env` is sourced by the runner script and must define:

- `TEST_COMMAND`: shell command executed inside the integration runner container

Optional fields:

- `RUNNER_SERVICE`: override the default runner service name (`integration-runner`)

Example:

```bash
TEST_COMMAND='go test ./pkg/mcp -run TestIntegration_RealConfiguredServer -v'
```

## Docker Conventions

Suite compose files can:

- define dependency services needed by the tests
- extend or override the shared `integration-runner` service
- inject environment variables into the runner for the tests to consume

The provided `mcp-streamable` suite is a good reference:

- it starts a local streamable HTTP MCP server
- wires the runner to that service through Docker networking
- runs the Go integration test against the containerized server

## Running Locally

Run all suites:

```bash
bash ./scripts/run-integration-tests.sh
```

Run one suite:

```bash
bash ./scripts/run-integration-tests.sh mcp-streamable
```

## Adding a New Suite

1. Create `integration/suites/<name>/docker-compose.yml`.
2. Create `integration/suites/<name>/suite.env`.
3. Put any fixture service code under `integration/fixtures/` if it is reusable.
4. Make sure the suite is self-contained and deterministic.
5. Validate it locally with `bash ./scripts/run-integration-tests.sh <name>`.

Once committed, the suite will be picked up automatically by the CI integration job.

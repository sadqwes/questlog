GO_IMAGE := golang:1.26
DOCKER_GO := docker run --rm -v "$(CURDIR)":/src -w /src $(GO_IMAGE)

.PHONY: up down reset test fmt vet vuln semgrep image scan check

up:          ## запустить локально: http://localhost:8080
	docker compose up -d --build

down:        ## остановить (данные сохраняются)
	docker compose down

reset:       ## остановить и стереть локальную базу
	docker compose down -v

test:        ## unit-тесты с race detector
	$(DOCKER_GO) go test -race ./...

fmt:
	$(DOCKER_GO) gofmt -w .

vet:
	$(DOCKER_GO) sh -c 'test -z "$$(gofmt -l .)" && go vet ./...'

vuln:        ## govulncheck, как в CI
	$(DOCKER_GO) sh -c 'go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...'

semgrep:     ## SAST, как в CI
	docker run --rm -v "$(CURDIR)":/src semgrep/semgrep semgrep scan --config p/golang --config p/owasp-top-ten --error --metrics=off /src

image:
	docker build -t questlog:local .

scan: image  ## Trivy по образу, как в CI
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --severity HIGH,CRITICAL --exit-code 1 --ignore-unfixed questlog:local

check: vet test vuln semgrep scan  ## всё, что проверяет CI

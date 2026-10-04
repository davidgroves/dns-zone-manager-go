# DNS Zone Manager Development Container (Go)
FROM mcr.microsoft.com/devcontainers/go:2.3-bookworm

# Remove expired yarn repo before apt-get update (present on some base images)
RUN rm -f /etc/apt/sources.list.d/yarn.list || true

# DNS tools for dig, nsupdate, rndc; psql for the scheduled change store;
# python3 + pip for pre-commit (hooks are language: system / Go + npm).
RUN apt-get update && apt-get install -y --no-install-recommends \
    bind9-dnsutils \
    bind9-host \
    bind9utils \
    postgresql-client \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# pre-commit (hooks invoke gofmt / go vet / golangci-lint / npm)
RUN pip3 install --break-system-packages --no-cache-dir 'pre-commit>=4.0.0'

# golangci-lint (used by make lint and pre-commit)
ARG GOLANGCI_LINT_VERSION=v2.14.0
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
    | sh -s -- -b /usr/local/bin "${GOLANGCI_LINT_VERSION}"

# Node.js 22
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Playwright browsers (Chromium, Firefox, WebKit) + OS dependencies.
# Install into a shared path so the vscode user finds them without a second
# download. iPad E2E projects use WebKit under the hood.
# Pin the installer to the lockfile version so the image matches package-lock.
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
COPY package.json package-lock.json .npmrc /tmp/playwright-install/
WORKDIR /tmp/playwright-install
RUN npm ci --ignore-scripts \
    && npx playwright install --with-deps \
    && chmod -R a+rwX /ms-playwright \
    && rm -rf /tmp/playwright-install /root/.npm
WORKDIR /

# Docker CLI + Compose plugin (for sibling containers and `docker compose`)
RUN curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/*

# DNS configuration files for easier CLI usage
# rndc.conf: allows 'rndc status' without -c flag
# dns-api-key.conf: allows 'nsupdate -k /etc/dns-api-key.conf' or 'dig -k /etc/dns-api-key.conf'
COPY .devcontainer/rndc.devcontainer.conf /etc/rndc.conf
COPY .devcontainer/dns-api-key.conf /etc/dns-api-key.conf

# Disable core dumps — Playwright/WebKit crashes otherwise leave multi-GB
# core.* files in the workspace when ulimit -c is unlimited.
RUN printf '%s\n' '# Soft limit for all sessions' '* soft core 0' '* hard core 0' \
        >> /etc/security/limits.conf \
    && printf '%s\n' 'ulimit -c 0' > /etc/profile.d/disable-coredumps.sh \
    && printf '\n# Disable core dumps (Playwright/WebKit can dump multi-GB files)\nulimit -c 0\n' \
        >> /home/vscode/.bashrc

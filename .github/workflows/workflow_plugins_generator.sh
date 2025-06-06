#!/bin/bash

# Disabled go-zero and hertz temporary
# plugins=("beego"  "chi"  "dotweb"  "echo"  "fiber"  "gin"  "goa"  "go-zero"  "hertz"  "kratos"  "roadrunner"  "souin"  "traefik"  "tyk"  "webgo")
plugins=("beego"  "chi"  "dotweb"  "echo"  "fiber"  "gin"  "goa"  "kratos"  "souin"  "traefik"  "webgo")
go_version=1.24

IFS= read -r -d '' tpl <<EOF
name: Build and validate Souin as plugins

on:
  - pull_request

jobs:
  build-caddy-validator:
    name: Caddy
    runs-on: ubuntu-latest
    services:
      redis:
        image: redis
        ports:
          - 6379:6379
      etcd:
        image: quay.io/coreos/etcd:v3.5.13
        env:
          ETCD_NAME: etcd0
          ETCD_ADVERTISE_CLIENT_URLS: http://etcd:2379,http://etcd:4001
          ETCD_LISTEN_CLIENT_URLS: http://0.0.0.0:2379,http://0.0.0.0:4001
          ETCD_INITIAL_ADVERTISE_PEER_URLS: http://etcd:2380
          ETCD_LISTEN_PEER_URLS: http://0.0.0.0:2380
          ETCD_INITIAL_CLUSTER_TOKEN: etcd-cluster-1
          ETCD_INITIAL_CLUSTER: etcd0=http://etcd:2380
          ETCD_INITIAL_CLUSTER_STATE: new
        ports:
          - 2379:2379
          - 2380:2380
          - 4001:4001
    steps:
      -
        name: Add domain.com host to /etc/hosts
        run: |
          sudo echo "127.0.0.1 domain.com etcd redis" | sudo tee -a /etc/hosts
      -
        name: Install Go
        uses: actions/setup-go@v3
        with:
          go-version: '$go_version'
      -
        name: Checkout code
        uses: actions/checkout@v4
      -
        name: Install xcaddy
        run: go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
      -
        name: Build Souin as caddy module
        run: cd plugins/caddy && xcaddy build --with github.com/darkweak/souin/plugins/caddy=./ --with github.com/darkweak/souin=../.. --with github.com/darkweak/storages/badger/caddy --with github.com/darkweak/storages/etcd/caddy --with github.com/darkweak/storages/nats/caddy --with github.com/darkweak/storages/nuts/caddy --with github.com/darkweak/storages/olric/caddy --with github.com/darkweak/storages/otter/caddy --with github.com/darkweak/storages/redis/caddy
      -
        name: Run Caddy tests
        run: cd plugins/caddy && go test -v ./...
      -
        name: Run detached caddy
        run: cd plugins/caddy && ./caddy run &
      -
        name: Run Caddy E2E tests
        uses: matt-ball/newman-action@master
        with:
          collection: "docs/e2e/Souin E2E.postman_collection.json"
          folder: '["Caddy"]'
          delayRequest: 5000
      -
        name: Run detached caddy
        run: cd plugins/caddy && ./caddy stop
      -
        name: Run detached caddy
        run: cd plugins/caddy && ./caddy run --config ./configuration.json &
      -
        name: Run Caddy E2E tests
        uses: matt-ball/newman-action@master
        with:
          collection: "docs/e2e/Souin E2E.postman_collection.json"
          folder: '["Caddy"]'
          delayRequest: 5000
  run-cache-tests:
    needs: build-caddy-validator
    name: Run cache-tests suite requirements and add the generated screenshot to the PR
    runs-on: ubuntu-latest
    steps:
      -
        name: Add domain.com host to /etc/hosts
        run: |
          sudo echo "127.0.0.1 domain.com etcd redis" | sudo tee -a /etc/hosts
      -
        name: Install Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Install Node.js
        uses: actions/setup-node@v4
        with:
          node-version: 18

      - uses: pnpm/action-setup@v3
        name: Install pnpm
        with:
          version: latest
      -
        name: Checkout Souin code
        uses: actions/checkout@v4
        with:
          repository: darkweak/souin
          path: souin
      -
        name: Checkout cache-tests code
        uses: actions/checkout@v4
        with:
          repository: http-tests/cache-tests
          path: cache-tests
      -
        name: Install xcaddy
        run: go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
      -
        name: Build Souin as caddy module for current commit
        run: cd souin/plugins/caddy && xcaddy build --with github.com/darkweak/souin/plugins/caddy=./ --with github.com/darkweak/souin=../.. --with github.com/darkweak/storages/badger/caddy --with github.com/darkweak/storages/etcd/caddy --with github.com/darkweak/storages/nats/caddy --with github.com/darkweak/storages/nuts/caddy --with github.com/darkweak/storages/olric/caddy --with github.com/darkweak/storages/otter/caddy --with github.com/darkweak/storages/redis/caddy
      -
        name: Run detached caddy
        run: cd souin/plugins/caddy && ./caddy run --config ../../docs/cache-tests/cache-tests-caddyfile --adapter caddyfile &
      -
        name: Sync index.mjs from souin to cache-tests
        run: cp souin/docs/cache-tests/index.mjs cache-tests/results/index.mjs
      -
        name: Run detached cache-tests server
        run: cd cache-tests && pnpm install && pnpm run server &
      -
        name: Run cache-tests test suite
        run: cd cache-tests && ./test-host.sh 127.0.0.1:4443 > results/mr.json
      - 
        name: install puppeteer-headful
        uses: mujo-code/puppeteer-headful@master
        env:
          CI: 'true'
      - 
        name: screenshots-ci-action
        uses: flameddd/screenshots-ci-action@master
        with:
          url: http://127.0.0.1:8000/
          devices: iPad Pro landscape
          noDesktop: true
          noCommitHashFileName: true
          fullPage: true
          releaseId: 144343803
        env:
          GITHUB_TOKEN: \${{ secrets.GITHUB_TOKEN }}
      - 
        name: Upload screenshot artifacts
        id: screenshot-uploader
        uses: edunad/actions-image@master
        with:
            path: screenshots/iPad_Pro_landscape.jpeg
            GITHUB_TOKEN: \${{ secrets.GITHUB_TOKEN }}
            title: 'cache-tests suite result'

EOF
workflow+="$tpl"

for i in ${!plugins[@]}; do
  lower="${plugins[$i]}"
  capitalized="$(tr '[:lower:]' '[:upper:]' <<< ${lower:0:1})${lower:1}"
  IFS= read -d '' tpl <<EOF
  build-$lower-validator:
    uses: ./.github/workflows/plugin_template.yml
    secrets: inherit
    with:
      CAPITALIZED_NAME: $capitalized
      LOWER_NAME: $lower
      GO_VERSION: '$go_version'
EOF
  workflow+="$tpl"
done
echo "${workflow%$'\n'}" >  "$( dirname -- "$0"; )/plugins.yml"

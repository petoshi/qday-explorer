# QDAY EXPLORER

Live: [explorer.pqday.com](https://explorer.pqday.com)

Explorer and read-only API for QDAY mainnet blocks, transactions, addresses and consensus status.

## Run it

You need Go 1.26, a C compiler for SQLite and the QDAY mainnet manifest.

```sh
git clone --recurse-submodules https://github.com/petoshi/qday-explorer.git
cd qday-explorer
go build -o qday-explorer .
./qday-explorer \
  -network qday/qday-mainnet.json \
  -data explorer-data \
  -listen 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`.

For a server already running a QDAY node, connect the indexer to that node over loopback:

```sh
./qday-explorer \
  -network /etc/qday/qday-mainnet.json \
  -data /var/lib/qday-explorer \
  -listen 127.0.0.1:8080 \
  -peers 127.0.0.1:19771 \
  -node-status http://127.0.0.1:19770/api/network-status
```

Put a reverse proxy in front of port 8080. The explorer's P2P listener is loopback-only and accepts no inbound peers.

## Public API

All routes are read-only JSON.

| Route | Result |
| --- | --- |
| `GET /api/status` | tip, index height, node connections, difficulty, hash rate, supply and QDAY state |
| `GET /api/blocks?limit=20&offset=0` | latest canonical blocks |
| `GET /api/blocks/{height-or-id}` | block header, reward, fees and transactions |
| `GET /api/transactions/recent?limit=20&offset=0` | recent transfers, proofs and mempool entries |
| `GET /api/transactions/{id}` | inputs, outputs, fee, block and DEFEND nonce |
| `GET /api/addresses/{qday-address}` | spendable balance, maturity, shield/decay state and history |
| `GET /api/search?q={value}` | resolved explorer path for a height, hash or address |
| `GET /healthz` | process health |
| `GET /readyz` | chain and address index readiness |

Amounts include a formatted QDAY value and the exact atomic integer.

## Network

This explorer accepts the fixed QDAY mainnet manifest. Its genesis ID is:

```text
d71aebcb687c2fca4d3a5819e6c632efa7d46731395970fce081f3dc57606a40
```

Node and wallet: [github.com/petoshi/qday](https://github.com/petoshi/qday)  
Website: [pqday.com](https://pqday.com)  
Downloads: [QDAY releases](https://github.com/petoshi/qday/releases/latest)  
Developer: [@_petoshi](https://x.com/_petoshi)

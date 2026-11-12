# ISO Connector Revarm (Go 1.27)

Reimplementation Go dari `alcorn-iso-connection` (client) dan `sdt-iso-adapter` (server). Packer memuat definisi ALTO secara langsung dari `cfg/packager/iso_spec_alto.xml`; tidak ada field layout yang di-hard-code.

## Modes

| `MODE` | Behavior |
|---|---|
| `client` | HTTP API `POST /call` → ISO TCP ke `SERVER_HOST:SERVER_PORT` |
| `server` | ISO TCP listener → HTTP `MESSAGE_URL` |
| `both` (default) | Menjalankan keduanya |

## Environment

Dua template terpisah tersedia agar deployment client dan server tidak tercampur:

| File | Peran | Konfigurasi kunci |
|---|---|---|
| `.env.client.example` | HTTP-to-ISO client | `HTTP_LISTEN_ADDR`, `SERVER_HOST`, `SERVER_PORT`, `NETWORK_TIMEOUT` |
| `.env.server.example` | ISO TCP server | `ISO_LISTEN_ADDR`, `MESSAGE_URL` |

Salin template sesuai peran, lalu isi endpoint environment target tanpa memasukkan kredensial ke Git.

```bash
cp .env.client.example .env.client
# atau
cp .env.server.example .env.server
```

`NETWORK_TIMEOUT` dan `INITIAL_NETWORK_HANDSHAKE_TIMEOUT` menerima duration Go (`60s`) atau milidetik (`60000`). Untuk kompatibilitas dengan `sdt-iso-adapter`, client menunggu initial network request `0800/001`, mengirim `0810/00`, baru kemudian mengirim business request `0200`. Set `INITIAL_NETWORK_HANDSHAKE_TIMEOUT=2s` pada client SDT. TCP framing mengikuti jPOS `PostChannel`: **2-byte big-endian message length**, lalu payload ISO ASCII.

## API client

```bash
curl -X POST http://localhost:8085/call \
  -H 'Content-Type: application/json' \
  -d '{"0":"0200","3":"340000","4":"000000001000","7":"0101123456","11":"000123","37":"ABC123DEF456","41":"TERM0001","49":"IDR","104":"XXX1231234567890"}'
```

Payload memakai nomor field ISO sebagai key string. Response adalah field ISO response sebagai JSON.

## Server mapping

- `0800` → `0810`, field 39 `00`.
- `0200`, field 3 prefix `34` → `inquiry` ke `MESSAGE_URL`.
- `0200`, field 3 prefix `40` → `disbursement` ke `MESSAGE_URL`.
- Upstream HTTP non-200/failure → ISO field 39 `91`.
- Request lain → field 39 `12`.

## Logging transaksi

Aplikasi menulis structured logs ke `stdout` untuk seluruh boundary request/response:

- HTTP API masuk dan response HTTP API;
- koneksi TCP ISO client/server;
- ISO request dan ISO response pada kedua arah;
- HTTP request/response menuju `MESSAGE_URL`, termasuk command dan status HTTP.

Setiap ISO event memuat MTI dan field ISO dalam JSON. Field sensitif tidak pernah dicetak: PAN (2), track data (35/36/45), private/additional data (48/55), PIN/security data (52/53), serta account/free-form data (102/103/104) menjadi `***REDACTED***`.

## Run and verify

```bash
go test ./...
go vet ./...
go build ./cmd/iso-connector
MODE=both MESSAGE_URL=http://localhost:8088/process go run ./cmd/iso-connector
```

# ISO Connector Revarm (Go 1.27)

Implementasi konektor ISO 8583 dengan definisi field yang dimuat dari XML packager. Aplikasi mendukung peran client, server, atau keduanya tanpa hard-code layout field.

## Modes

| `MODE` | Behavior |
|---|---|
| `client` | HTTP API `POST /call` → ISO TCP ke `SERVER_HOST:SERVER_PORT` |
| `server` | ISO TCP listener → HTTP `MESSAGE_URL` |
| `both` (default) | Menjalankan keduanya |

## Environment

Gunakan template sesuai peran:

| File | Peran | Konfigurasi kunci |
|---|---|---|
| `.env.client.example` | HTTP-to-ISO client | `HTTP_LISTEN_ADDR`, `SERVER_HOST`, `SERVER_PORT`, `NETWORK_TIMEOUT`, `PACKAGER_PATH=cfg/packager/iso_spec.xml` |
| `.env.server.example` | ISO TCP server | `ISO_LISTEN_ADDR`, `MESSAGE_URL`, `PACKAGER_PATH=cfg/packager/iso_spec.xml` |

```bash
cp .env.client.example .env.client
# atau
cp .env.server.example .env.server
```

`NETWORK_TIMEOUT` dan `INITIAL_NETWORK_HANDSHAKE_TIMEOUT` menerima duration Go (`60s`) atau milidetik (`60000`). Saat TCP terhubung, client merespons setiap request network-management awal dengan `0810/00` sebelum mengirim transaksi `0200`. TCP framing menggunakan 2-byte big-endian message length diikuti payload ISO ASCII.

## API client

```bash
curl -X POST http://localhost:8085/call \
  -H 'Content-Type: application/json' \
  -d '{"0":"0200","3":"340000","4":"000000001000","7":"0101123456","11":"000123","37":"ABC123DEF456","41":"TERM0001","49":"IDR","104":"XXX1231234567890"}'
```

Payload memakai nomor field ISO sebagai key string. Response berisi field ISO sebagai JSON.

## Server `MESSAGE_URL`

Untuk MTI `0200` dengan DE3 prefix `34` atau `40`, server mengirim seluruh field ISO sebagai JSON langsung ke `MESSAGE_URL`:

```http
POST /process
Content-Type: application/json
```

```json
{
  "0": "0200",
  "2": "1234567890123456",
  "3": "340000",
  "4": "000000001000",
  "7": "1113214500",
  "11": "000123",
  "12": "214500",
  "13": "1113",
  "15": "1113",
  "17": "1113",
  "18": "6012",
  "32": "886",
  "33": "886",
  "37": "ABC123DEF456",
  "41": "TERM0001        ",
  "42": "MERCHANT0000001",
  "43": "SIMULATOR MERCHANT JAKARTA              ",
  "49": "IDR",
  "104": "XXX1231234567890"
}
```

Nilai fixed-width mengikuti hasil unpack ISO. Karena itu, field seperti DE41 dapat mengandung trailing spaces.

`MESSAGE_URL` harus mengembalikan HTTP `200` dengan JSON field ISO response. DE0 dan DE39 wajib tersedia:

```json
{
  "0": "0210",
  "3": "340000",
  "11": "000123",
  "37": "ABC123DEF456",
  "38": "ABC123",
  "39": "00"
}
```

Semua key harus berupa nomor field `0`–`128` dan semua value harus berupa string. Response non-200, JSON tidak valid, key tidak valid, DE0 tidak berisi 4 karakter, atau DE39 tidak berisi 2 karakter dipetakan menjadi response ISO DE39 `91`.

Routing server:

- `0800` → `0810`, field 39 `00`, tanpa memanggil `MESSAGE_URL`.
- `0200`, field 3 prefix `34` atau `40` → forward JSON ISO ke `MESSAGE_URL`.
- HTTP/response `MESSAGE_URL` gagal → ISO field 39 `91`.
- Request lain → field 39 `12`.

## Logging transaksi

Aplikasi menulis structured logs ke `stdout` untuk boundary HTTP dan ISO TCP. Field sensitif tidak dicetak: PAN (2), track data (35/36/45), private/additional data (48/55), PIN/security data (52/53), serta account/free-form data (102/103/104) menjadi `***REDACTED***`.

## Run and verify

```bash
go test ./...
go vet ./...
go build ./cmd/iso-connector
MODE=both MESSAGE_URL=http://localhost:8088/process go run ./cmd/iso-connector
```

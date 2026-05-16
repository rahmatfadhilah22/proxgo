# Proxgo

`Proxgo` adalah gateway token ringan berbasis Go untuk client yang kompatibel dengan OpenAI API seperti Codex CLI. Service ini menyediakan satu `base_url` yang stabil, sementara token upstream dikelola di sisi server dengan strategi round-robin, cooldown, dan retry sederhana.

## Fitur Utama

- Validasi akses client memakai `GATEWAY_API_KEY`
- Rotasi token upstream secara round-robin
- Cooldown token otomatis saat kena `429`
- Retry ke token lain saat `429`, `5xx`, atau error transport sementara
- Batas ukuran body request untuk mencegah lonjakan memory
- Tetap model-agnostic, body request diteruskan apa adanya

## Struktur Project

```text
.
├── main.go
├── .env.example
├── internal/
│   ├── auth/
│   ├── config/
│   ├── httpjson/
│   ├── pool/
│   └── proxy/
└── docs/
    └── superpowers/
```

## Konfigurasi

Salin `.env.example` menjadi `.env`, lalu isi:

```env
GATEWAY_API_KEY=replace_with_a_long_random_value
UPSTREAM_TOKENS=token_one,token_two
UPSTREAM_BASE_URL=https://ai.patungin.id/v1
PORT=8080
TOKEN_COOLDOWN_SECONDS=60
```

Keterangan:

- `GATEWAY_API_KEY`: token yang dipakai client untuk mengakses gateway
- `UPSTREAM_TOKENS`: daftar token upstream, pisahkan dengan koma
- `UPSTREAM_BASE_URL`: alamat upstream OpenAI-compatible
- `PORT`: port HTTP gateway
- `TOKEN_COOLDOWN_SECONDS`: lama cooldown token setelah menerima `429`

## Menjalankan

```bash
go test ./...
go run .
```

Build binary:

```bash
go build -o token-gateway .
```

Build Docker image:

```bash
docker build -t proxgo .
```

Jalankan container:

```bash
docker run --rm -p 8080:8080 \
  -e GATEWAY_API_KEY=replace_with_a_long_random_value \
  -e UPSTREAM_TOKENS=token_one,token_two \
  proxgo
```

Atau pakai Docker Compose yang membaca `.env`:

```bash
cp .env.example .env
docker compose up --build
```

## Endpoint Tambahan

- `GET /healthz` mengembalikan `200 {"status":"ok"}` tanpa membutuhkan `Authorization` header

## Batas Request

- Body request dibatasi sampai `1 MiB`
- Jika melewati batas, gateway mengembalikan `413 {"error":"request body too large"}`

## Catatan

- Gateway hanya mengganti header `Authorization` ke token upstream terpilih.
- Field seperti `model`, `messages`, `stream`, dan parameter lain tidak diubah.
- Error yang berasal dari gateway sendiri dibalas dalam format JSON `{"error":"..."}`.

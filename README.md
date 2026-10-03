# Kite 🪁

> พัฒนาโดย **TF Soft Co., Ltd.** · https://github.com/tfsoft-tech/kite

เว็บเฟรมเวิร์กสำหรับ Go ที่เล็ก เร็ว และใช้ทรัพยากรน้อย สร้างบน `net/http` ล้วน ไม่มี dependency ภายนอก

- Router แบบ radix tree ที่ไม่จองหน่วยความจำ (0 allocs) ทุกเส้นทาง ทั้ง static, `:param` และ `*wildcard`
- `Ctx` และตัวห่อ ResponseWriter ถูก pool ไว้ ทำให้ hot path ไม่สร้าง garbage
- Middleware ถูกประกอบครั้งเดียวตอนลงทะเบียน route จึงไม่ต้องไล่ chain ทุก request
- Handler คืน `error` แล้วมี `ErrorHandler` กลางจัดรูปแบบให้ ไม่เผยข้อความ error ภายในให้ client
- ใช้ร่วมกับ `http.Server`, HTTP/2, TLS, `httptest` และ `http.Handler` เดิมได้ทั้งหมด
- โค้ดหลักประมาณ 900 บรรทัด อ่านจบได้ในหนึ่งชั่วโมง

## ติดตั้ง

```bash
go get github.com/tfsoft-tech/kite@latest
```

ถ้า repository เป็น Private ให้ตั้งค่าก่อนหนึ่งครั้ง: `go env -w GOPRIVATE=github.com/tfsoft-tech/*`

## เริ่มต้นใช้งาน

```go
app := kite.New()
app.Use(kite.Recover(), kite.RequestID(), kite.Logger())

app.GET("/users/:id", func(c *kite.Ctx) error {
    return c.JSON(map[string]string{"id": c.Param("id")})
})

api := app.Group("/api/v1", kite.Timeout(5*time.Second))
api.POST("/todos", createTodo)

app.Run(":8080") // หยุดแบบ graceful เมื่อได้ SIGINT/SIGTERM
```

ตัวอย่างเต็มอยู่ที่ `examples/todo` (รันด้วย `go run ./examples/todo`)

### API หลัก

| กลุ่ม | ฟังก์ชัน |
|---|---|
| Routing | `GET/POST/PUT/PATCH/DELETE/Handle`, `Group`, `Static`, `WrapHandler` |
| Request | `Param`, `ParamInt`, `Query`, `Header`, `Bind` (JSON + จำกัดขนาด body), `Set/Get`, `Route` |
| Response | `JSON`, `String`, `HTML`, `Bytes`, `Stream`, `Status`, `NoContent`, `Redirect` |
| Errors | `kite.NewError(code, msg)`, `ErrNotFound`, `ErrUnauthorized`, ... |
| Middleware | `Recover`, `Logger` (slog), `RequestID`, `CORS`, `Timeout` |
| Config | `BodyLimit`, `ErrorHandler`, `NotFound`, `JSONMarshal` (เสียบ sonic/go-json ได้), `RedirectTrailingSlash`, timeouts |

ความสามารถอื่น: ตอบ 405 พร้อม header `Allow`, HEAD ใช้ handler ของ GET ได้อัตโนมัติ, server มีค่า timeout ที่ปลอดภัยไว้ให้แล้ว

## ผลเบนช์มาร์ก

วัดบนเครื่อง 2 vCPU ด้วย Go 1.24.7 เทียบกับ Gin v1.10.1, Echo v4.13.3, httprouter v1.3.0, Chi v5.3.2 และ `http.ServeMux` ของ Go เอง
ทุกตัวใช้ GitHub API ชุดเดียวกัน 189 routes และผ่านการตรวจว่า match ครบทุก route (ค่ามัธยฐานจาก 3 รอบ)

### Routing ล้วน (ns/op, ยิ่งน้อยยิ่งดี)

| Benchmark | **Kite** | Gin | Echo | httprouter | Chi | ServeMux |
|---|---|---|---|---|---|---|
| Static `/user/repos` | **54** | 61 | 80 | 45 | 410 | 170 |
| 1 param | **65** | 61 | 83 | 94 | 683 | 222 |
| 4 params | **100** | 100 | 151 | 155 | 871 | 609 |
| ทั้ง 189 routes | **15,421** | 16,349 | 23,722 | 21,035 | 149,826 | 83,412 |
| allocs ใน 189 routes | **0** | 0 | 0 | 153 | 684 | 306 |

### อ่าน param แล้วตอบ JSON

| | **Kite** | Gin | Echo | Chi | ServeMux |
|---|---|---|---|---|---|
| ns/op | **284** | 334 | 383 | 921 | 472 |
| allocs/op | **1** | 3 | 2 | 6 | 3 |

### ยิงจริงผ่าน HTTP (GOMAXPROCS=1, 64 connections, เฉลี่ย 2 รอบ)

| | **Kite** | Gin | Echo | Chi | ServeMux |
|---|---|---|---|---|---|
| req/s | **50,965** | 43,849 | 45,970 | 43,623 | 46,006 |
| RAM ตอนว่าง | **6.7 MB** | 11.1 MB | 6.9 MB | 6.9 MB | 7.1 MB |
| RAM สูงสุดตอนโหลด | 13.3 MB | 16.3 MB | 13.3 MB | 13.8 MB | 13.5 MB |
| ขนาด binary | 5.8 MB | 8.2 MB | 6.1 MB | 6.0 MB | 5.8 MB |

### อ่านผลอย่างตรงไปตรงมา

- Kite ทำความเร็วระดับเดียวกับ Gin ซึ่งเป็นกลุ่มที่เร็วที่สุด และชนะเมื่อวัดแบบรวมทุก route และแบบตอบ JSON ส่วน httprouter ยังเร็วกว่าเล็กน้อยใน static route เดี่ยว
- ใน request จริง ต้นทุนส่วนใหญ่อยู่ที่ `net/http` และเครือข่าย ส่วนต่างระหว่างเฟรมเวิร์กจึงเหลือราว 10–15% และตัวเลขนี้แกว่งได้ตามเครื่อง
- RAM ตอนว่างและขนาด binary ใกล้เคียงกับ stdlib เพราะ Kite ไม่มี dependency เลย
- ถ้าต้องการเร็วกว่านี้มาก ทางเลือกต่อไปคือเปลี่ยนชั้นล่างจาก `net/http` เป็น `fasthttp` หรือใช้ JSON encoder ที่เร็วกว่า (`Config.JSONMarshal`) แต่จะแลกกับความเข้ากันได้กับ ecosystem ของ `net/http`

รันซ้ำได้ด้วย:

```bash
cd bench && GOFLAGS=-mod=mod go test -bench . -count 3
```

## ข้อควรรู้

- อย่าเก็บ `*kite.Ctx` ไว้ใช้หลัง handler จบ หรือส่งต่อให้ goroutine อื่น เพราะ object ถูกนำกลับไปใช้ซ้ำ ให้คัดลอกค่าที่ต้องใช้ออกมาก่อน
- ต้องเรียก `app.Use` ก่อนลงทะเบียน route (ถ้าเรียกทีหลังจะ panic เพื่อไม่ให้พลาดแบบเงียบๆ)
- route ที่ชนกัน เช่น `/a/:id` กับ `/a/:name` จะ panic ตอนเริ่มโปรแกรม ไม่ใช่ตอนรับ request
- สถานะปัจจุบันเป็น prototype มี unit test และ race test ผ่านครบ แต่ยังไม่ผ่านการใช้งานจริงใน production

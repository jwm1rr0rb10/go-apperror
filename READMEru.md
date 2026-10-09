# apperror

Структурированные ошибки для Go-сервисов с gRPC и HTTP API.

Один тип `*AppError` содержит тип ошибки, сообщение для клиента, прикладной код, нарушения по полям,
подсказку для повтора и trace ID. Он превращается в gRPC-статус (с деталями `ErrorInfo`, `BadRequest`
и `RetryInfo`) и в JSON-ответ HTTP. `ErrorHandler` применяет единую политику к gRPC- и HTTP-серверам:
логирование, отчёты об ошибках, перехват паник и запись в OpenTelemetry-спан.

---

## Возможности

- 13 типов ошибок с маппингом на gRPC-коды и HTTP-статусы (см. [Типы ошибок](#типы-ошибок))
- gRPC: `GRPCStatus()` с `ErrorInfo` (reason `SYSTEMCODE-CODE`), нарушениями полей в `BadRequest`
  и `RetryInfo`; `FromGRPCStatus()` / `FromGRPCError()` восстанавливают `*AppError` на клиенте
- HTTP: JSON-тело, тип сериализуется именем, заголовок `Retry-After`
- `Message` для клиента никогда не берётся из обёрнутой ошибки, поэтому внутренние детали не утекают
- `errors.Is` / `errors.As` / `errors.Unwrap` и хелперы вида `IsNotFound(err)`, которые понимают
  и ошибки, полученные от gRPC-клиентов
- `ErrorHandler` для gRPC (unary- и stream-интерцепторы) и HTTP (адаптер хендлеров и middleware):
  уровень лога по типу ошибки, обработка отмены контекста, перехват паник, запись в OpenTelemetry-спан,
  подключаемый репортер ошибок
- Stack trace для внутренних ошибок и паник, который подхватывают Sentry и OpenTelemetry
- Реализация `slog.LogValuer` для структурных логов
- Зависимости: gRPC, genproto, OpenTelemetry, [go-errors](https://github.com/jwm1rr0rb10/go-errors)
  (`OneLine`, `Wrap`). Sentry нужен только опциональному пакету `sentryreport`

---

## Установка

```bash
go get -u github.com/jwm1rr0rb10/libraries/backend/golang/apperror
```

---

## Быстрый старт

```go
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/jwm1rr0rb10/libraries/backend/golang/apperror"
	"github.com/jwm1rr0rb10/libraries/backend/golang/apperror/sentryreport"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
)

var ErrUserNotFound = apperror.NewNotFoundError("USERS",
	apperror.WithCode(1),
	apperror.WithMessage("user not found"),
)

func findUser(ctx context.Context, id string) error {
	return ErrUserNotFound.WithTrace(ctx)
}

func main() {
	h := apperror.NewErrorHandler("USERS", apperror.WithReporter(sentryreport.Report))

	// gRPC: хендлеры просто возвращают *AppError, он реализует GRPCStatus().
	// OTel stats handler создаёт спан до запуска интерцепторов.
	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(h.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(h.StreamServerInterceptor()),
	)
	_ = grpcServer

	// HTTP: хендлеры возвращают ошибку, адаптер пишет её как JSON.
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", h.HTTP(func(w http.ResponseWriter, r *http.Request) error {
		if err := findUser(r.Context(), r.PathValue("id")); err != nil {
			return err // 404 {"message":"user not found","system_code":"USERS","type":"NotFound","code":1}
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))

	// otelhttp снаружи, чтобы спан уже существовал, когда обрабатывается ошибка.
	log.Fatal(http.ListenAndServe(":8080", otelhttp.NewHandler(mux, "users")))
}
```

HTTP-часть проверяется в `example_test.go`.

---

## Создание ошибок

```go
err := apperror.NewNotFoundError("USERS",
	apperror.WithCode(1),
	apperror.WithMessage("user not found"),
	apperror.WithDomain("users"),
	apperror.WithTrace(ctx),
)
```

| Опция | Назначение |
|---|---|
| `WithErr(error)` | Обернуть исходную причину (видна в логах и трейсах, клиенту не отправляется) |
| `WithMessage(string)` | Сообщение для клиента (по умолчанию — безопасное для типа) |
| `WithDomain(string)` | Бизнес-домен |
| `WithCode(uint32)` | Прикладной числовой код |
| `WithFields(ErrorFields)` | Добавить нарушения из map `поле -> описание`, отсортированные по полю |
| `WithViolations(...FieldViolation)` | Добавить нарушения с кодами причины, в заданном порядке |
| `WithRetryAfter(time.Duration)` | Когда клиенту повторить запрос (gRPC `RetryInfo`, HTTP `Retry-After`) |
| `WithTrace(context.Context)` | Прикрепить trace ID из OpenTelemetry |
| `WithTraceID(string)` | Прикрепить trace ID вручную |

Методы `WithTrace`, `WithFields` и `WithViolations` возвращают копию, поэтому sentinel-ошибки на уровне
пакета можно безопасно дополнять.

### Sentinel-ошибки и `errors.Is`

`errors.Is` сравнивает тип, system code и code. Ошибки без кода (0) различаются только сообщением,
поэтому для них сравнивается и оно. Задавайте sentinel-ошибкам код, чтобы сравнение не зависело от текста:

```go
var ErrUserNotFound = apperror.NewNotFoundError("USERS", apperror.WithCode(1), apperror.WithMessage("user not found"))

if errors.Is(err, ErrUserNotFound) { ... } // совпадает и с ErrUserNotFound.WithTrace(ctx)
```

### Типы ошибок

| Конструктор | `Type` | HTTP | gRPC |
|---|---|---|---|
| `NewInternalError` | `TypeInternal` | 500 | `Internal` |
| `NewBadRequestError` | `TypeBadRequest` | 400 | `InvalidArgument` |
| `NewValidationError` | `TypeValidation` | 400 | `InvalidArgument` |
| `NewNotFoundError` | `TypeNotFound` | 404 | `NotFound` |
| `NewUnauthorizedError` | `TypeUnauthorized` | 401 | `Unauthenticated` |
| `NewForbiddenError` | `TypeForbidden` | 403 | `PermissionDenied` |
| `NewConditionFailedError` | `TypeConditionFailed` | 412 | `FailedPrecondition` |
| `NewConflictError` | `TypeConflict` | 409 | `AlreadyExists` |
| `NewTooManyRequestsError` | `TypeTooManyRequests` | 429 | `ResourceExhausted` |
| `NewUnavailableError` | `TypeUnavailable` | 503 | `Unavailable` |
| `NewTimeoutError` | `TypeTimeout` | 504 | `DeadlineExceeded` |
| `NewCanceledError` | `TypeCanceled` | 499 | `Canceled` |
| `NewNotImplementedError` | `TypeNotImplemented` | 501 | `Unimplemented` |

В JSON тип сериализуется именем: `"type": "NotFound"`.

### Проверка типа

```go
if apperror.IsNotFound(err) { ... }            // работает и через обёртку fmt.Errorf("%w")
if apperror.IsType(err, apperror.TypeConflict) { ... }
if t, ok := apperror.TypeOf(err); ok { ... }
```

Хелперы работают и для ошибок от gRPC-клиентов: тип восстанавливается из `ErrorInfo`, а для обычного
статуса определяется по gRPC-коду.

### Сообщение и причина

`Message` — то, что видит клиент (сообщение gRPC-статуса, поле `message` в JSON). Оно **никогда** не
берётся из обёрнутой ошибки: ошибки драйверов могут содержать SQL, хосты или учётные данные. Без
`WithMessage` используется безопасное сообщение по умолчанию для типа (`"internal error"`, `"not found"`, ...).

Причина остаётся доступной для логов и трейсов:

```go
err := apperror.NewInternalError("USERS", apperror.WithCode(2), apperror.WithErr(dbErr))
err.Message // "internal error"
err.Error() // "internal error: pq: connection refused (code: USERS-2)"
```

Причина-мультиошибка (например, `errors.Join`) выводится в одну строку: `"internal error: timeout; dial: refused"`.

`Error()` намеренно не включает нарушения полей и trace ID: значения с высокой кардинальностью в строке
ошибки ломают группировку в Sentry и агрегаторах логов. `*AppError` реализует `slog.LogValuer`, поэтому
структурный логгер получает `message`, `type`, `system_code`, `code`, `cause`, `domain`, `violations`,
`trace_id` и `retry_after`.

### Нарушения по полям

```go
err := apperror.NewValidationError("USERS",
	apperror.WithFields(apperror.ErrorFields{"email": "required"}),
	apperror.WithViolations(apperror.FieldViolation{
		Field:       "password",
		Reason:      "TOO_SHORT", // стабильный код, по нему клиент может локализовать текст
		Description: "min 8 chars",
	}),
)
```

```json
{"message":"validation failed","system_code":"USERS","type":"Validation","code":0,
 "violations":[{"field":"email","description":"required"},
               {"field":"password","reason":"TOO_SHORT","description":"min 8 chars"}]}
```

У одного поля может быть несколько нарушений. В gRPC они передаются как `BadRequest.FieldViolations`.

### Подсказка для повтора

```go
err := apperror.NewTooManyRequestsError("USERS", apperror.WithRetryAfter(1500*time.Millisecond))
```

Задержка отправляется как gRPC `RetryInfo` и как HTTP-заголовок `Retry-After` (округляется вверх до
целых секунд: `2`). `FromGRPCStatus` её восстанавливает, поэтому задержка от другого сервиса доходит до HTTP-клиента.

### Stack trace

Внутренние ошибки (`NewInternalError`) сохраняют стек вызова конструктора, перехваченные паники — стек
места паники. Стек доступен через `StackTrace() []uintptr`, который распознаёт Sentry, и добавляется
в событие exception в OpenTelemetry. Остальные типы стек не снимают, чтобы частые клиентские ошибки
оставались дешёвыми.

---

## ErrorHandler

`ErrorHandler` применяет одну политику к gRPC- и HTTP-серверам:

| Возвращённая ошибка | Ответ | Уровень лога | Отчёт | Статус спана |
|---|---|---|---|---|
| `context.Canceled` (в том числе обёрнутая, например во внутренний `*AppError`) | `Canceled` (HTTP 499) | Info | нет | не меняется |
| `context.DeadlineExceeded` (в том числе обёрнутая) | `DeadlineExceeded` (HTTP 504) | Warn | нет | `Error` |
| `*AppError` клиентского типа (4xx) | как есть | Info | нет | не меняется |
| `*AppError` `TypeTimeout` | как есть | Warn | нет | `Error` |
| `*AppError` `TypeInternal` | как есть | Error | да | `Error` |
| прочие серверные типы (`Unavailable`, `NotImplemented`) | как есть | Error | нет | `Error` |
| gRPC status error (например, от вызова другого сервиса) | как есть | по коду | нет | по коду |
| любая другая ошибка | внутренний `*AppError` "unknown internal system error" | Error | да | `Error` |
| паника | внутренний `*AppError` "internal error" | Error, с `panic` и `stack` | да | `Error` |

Каждая запись лога содержит `method` (полное имя gRPC-метода или шаблон HTTP-маршрута) и `error`.
«Отчёт» означает отправку в `Reporter`, если он задан.

```go
h := apperror.NewErrorHandler("USERS",
	apperror.WithLogger(logging.L),                // func(ctx) *slog.Logger; по умолчанию slog.Default()
	apperror.WithReporter(sentryreport.Report),    // по умолчанию отчёты не отправляются
	apperror.WithRequestAttr(func(req any) slog.Attr {
		return slog.String("request", redact(req)) // по умолчанию запрос не логируется (PII)
	}),
)
```

Сокращения без общего хендлера: `apperror.GRPCUnaryInterceptor("USERS", opts...)`,
`apperror.GRPCStreamInterceptor("USERS", opts...)`.

### gRPC

```go
grpc.NewServer(
	grpc.StatsHandler(otelgrpc.NewServerHandler()),
	grpc.ChainUnaryInterceptor(h.UnaryServerInterceptor()),
	grpc.ChainStreamInterceptor(h.StreamServerInterceptor()),
)
```

### HTTP

- `h.HTTP(fn)` превращает `func(w, r) error` в `http.Handler` и перехватывает паники.
  `fn` не должен писать ответ, если возвращает ошибку.
- `h.HTTPMiddleware(next)` перехватывает паники в обычном `http.Handler`, например во всём роутере.
- `h.WriteHTTPError(w, r, err)` пишет ошибку из уже существующего хендлера.
- Ошибка от вызова другого gRPC-сервиса конвертируется обратно: статус с `ErrorInfo` восстанавливается
  через `FromGRPCStatus`, обычный статус получает тип по коду и безопасное сообщение по умолчанию.

### Паники

Перехваченная паника становится внутренним `*AppError`: клиент получает `"internal error"`, значение
паники сохраняется как причина (`errors.Is` работает, если это была ошибка), stack trace начинается с места
паники, а в записи лога есть атрибуты `panic` и `stack`. `http.ErrAbortHandler` пробрасывается дальше,
как ожидает `net/http`.

### OpenTelemetry

`ErrorHandler` записывает упавшие запросы в спан из контекста запроса. Спан должен уже существовать,
поэтому `otelgrpc.NewServerHandler()` / `otelhttp.NewHandler` подключаются снаружи хендлера
(см. [Быстрый старт](#быстрый-старт)). Если спан не записывается, ничего не происходит.

| Ошибка | Атрибуты спана | Событие exception | Статус спана |
|---|---|---|---|
| клиентские ошибки (4xx), отмены | `app.error.type`, `app.error.system_code`, `app.error.code` | нет | не меняется |
| серверные сбои (5xx, включая таймауты), паники | те же | да, с `exception.stacktrace` для внутренних ошибок и паник | `Error` |

Так требуют semantic conventions OpenTelemetry: ответы 4xx не считаются ошибками сервера, а событие
исключения на каждый такой ответ под нагрузкой было бы шумом. `otelgrpc` / `otelhttp` выставляют итоговое
описание статуса при закрытии спана, поэтому подробности ошибки хранятся в событии exception.

### Sentry

Опциональный пакет `sentryreport` отправляет ошибку в Sentry hub из контекста запроса (или в текущий hub,
если его нет). Stack trace внутренних ошибок и паник подхватывается автоматически:

```go
apperror.WithReporter(sentryreport.Report)
```

---

## Хелперы для клиента

```go
// Вариант 1: проверить тип
if apperror.IsNotFound(err) { ... }

// Вариант 2: восстановить полный *AppError и сравнить с sentinel-ошибкой
if ae, ok := apperror.FromGRPCError(err); ok && errors.Is(ae, ErrUserNotFound) { ... }

// Вариант 3: маппинг ErrorInfo.Reason ("SYSTEMCODE-CODE") на доменные ошибки
st, _ := status.FromError(err)
mappedErr := apperror.ErrorInfoFromDetails(st, map[string]func() error{
	"USERS-1": func() error { return ErrUserNotFound },
})
```

`FromGRPCStatus(st)` делает то же, что `FromGRPCError`, для `*status.Status`.
`FromError(err)` извлекает копию `*AppError` из цепочки ошибок.

---

## Производительность

Замеры на Go 1.27, x86-64 (`go test -bench`):

| Операция | Время | Выделения памяти |
|---|---|---|
| `NewNotFoundError(...)` | 140 нс | 1 |
| `NewInternalError(...)` (снимает стек) | 690 нс | 2 |
| `IsNotFound(err)` для обёрнутого `*AppError` | 4 нс | 0 |
| `IsNotFound(err)` для ошибки gRPC-клиента | 2.3 мкс | 15 |
| `Error()` с причиной | 310 нс | 4 |
| `GRPCStatus()` с нарушениями полей | 3 мкс | 24 |
| unary-интерцептор, возвращён `NotFound` (JSON-логгер в `io.Discard`) | 3 мкс | 13 |

---

## Тестирование

```bash
go test -race ./...
go test -run=^$ -fuzz=FuzzParseReason -fuzztime=30s .
```

---

## Рекомендации

- Объявляйте sentinel-ошибки с `WithCode` + `WithMessage` и сравнивайте их через `errors.Is`
- Причину оборачивайте через `WithErr`, а текст для клиента задавайте через `WithMessage`, не наоборот
- Используйте `WithTrace(ctx)` в хендлерах и сервисах
- Используйте нарушения по полям вместо данных в сообщении; задавайте `Reason`, если клиенты локализуют ошибки
- Задавайте `WithRetryAfter` для ошибок `TooManyRequests` и `Unavailable`
- Возвращайте обычные ошибки только для действительно неожиданных сбоев: хендлер обернёт их и отправит отчёт
- Подключайте OpenTelemetry снаружи `ErrorHandler`; роутеры без `h.HTTP` оборачивайте в `h.HTTPMiddleware`
- На стороне клиента используйте хелперы вида `IsNotFound` или `FromGRPCError`

---

## Лицензия

[MIT License](https://github.com/jwm1rr0rb10/libraries/blob/main/backend/golang/LICENSE) – © Raman Zaitsau [@jwm1rrr0rb10](https://github.com/jwm1rr0rb10)

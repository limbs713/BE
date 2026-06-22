# BE

Gin 기반 REST API 백엔드.

## 요구 사항

- Go 1.22+ (개발 환경: go1.26.4)

## 구조

```
.
├── cmd/server/        # 엔트리포인트 (main.go)
└── internal/
    ├── router/        # 라우터 설정 (라우트 등록 지점)
    └── handler/       # HTTP 핸들러
```

## 실행

```bash
go run ./cmd/server
```

서버가 `:8080`에서 뜹니다. 동작 확인:

```bash
curl localhost:8080/health
# {"status":"ok"}
```

## 빌드

```bash
go build -o bin/server ./cmd/server
```

# CLOVA OCR 설계 초안

## 목표

- 프론트가 이미지를 업로드하면 BE가 원본 이미지를 DB에 저장한다.
- 저장된 이미지를 기준으로 BE가 NAVER CLOVA OCR API를 호출한다.
- OCR 결과 텍스트를 DB에 저장하고, 이후 광고 문구 검토 흐름에서 재사용한다.

## 권장 처리 흐름

1. `POST /upload-image`로 이미지를 업로드한다.
2. BE는 `ad_images`에 원본 이미지와 상태 `pending`을 저장한다.
3. 비동기 워커 또는 백그라운드 잡이 `pending` 이미지를 조회한다.
4. 워커는 상태를 `processing`으로 변경한 뒤 CLOVA OCR API를 호출한다.
5. 성공 시 추출 텍스트를 `ocr_text`에 저장하고 상태를 `done`으로 변경한다.
6. 실패 시 상태를 `failed`로 변경하고 `last_error`에 오류를 기록한다.

## 테이블 역할

`ad_images`

- `image_data`: 원본 이미지 바이너리
- `ocr_provider`: 현재 OCR 제공자. 지금은 `clova` 고정
- `ocr_status`: `pending`, `processing`, `done`, `failed`
- `ocr_text`: OCR 최종 추출 텍스트
- `clova_request_id`: CLOVA 요청 식별자 저장용
- `clova_raw_response`: 원본 응답 보관용(JSONB)
- `last_error`: 실패 원인 저장용
- `processed_at`: OCR 완료 또는 실패 처리 시각

## API 경계

### 1. 업로드 API

- Endpoint: `POST /upload-image`
- Content-Type: `multipart/form-data`
- Form field: `image`
- 역할: 저장만 수행하고, OCR은 바로 처리하지 않아도 된다.

응답 예시:

```json
{
  "id": "0f7c43d7-2ed7-442c-8adc-568d6b4880da",
  "file_name": "poster.png",
  "content_type": "image/png",
  "file_size_bytes": 183920,
  "ocr_provider": "clova",
  "ocr_status": "pending",
  "created_at": "2026-06-29T14:30:00Z"
}
```

### 2. OCR 실행 계층

권장 인터페이스:

```go
type OCRClient interface {
	ExtractText(ctx context.Context, image []byte, fileName string) (OCRResult, error)
}

type OCRResult struct {
	RequestID   string
	ExtractedText string
	RawResponse []byte
}
```

현재 제공자 구현체:

- `clovaClient`

## CLOVA OCR 호출 방식

필요 환경변수:

- `CLOVA_OCR_URL`
- `CLOVA_OCR_SECRET`

요청 방식:

- `POST {CLOVA_OCR_URL}`
- Header: `X-OCR-SECRET: {CLOVA_OCR_SECRET}`
- Body: `message` JSON + `file` multipart 업로드

Go 구현 시 핵심 포인트:

- `requestId`는 UUID로 생성
- `timestamp`는 현재 epoch millis 사용
- `images[0].format`은 확장자 기반으로 지정
- 응답의 `images[].fields[].inferText`를 공백으로 합쳐 `ocr_text` 저장

## 추천 후속 작업

1. `internal/image/clova.go`에 HTTP 클라이언트 구현
2. `internal/image/service.go`에 `ProcessPendingImage` 또는 `ProcessImageByID` 추가
3. 이미지 상태 조회 API `GET /images/:id` 추가
4. 장기적으로는 원본 이미지를 DB 대신 S3에 저장하고, DB에는 S3 key만 저장하도록 전환
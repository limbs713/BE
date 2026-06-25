package rag

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// clampScore 는 위험도 점수를 0~100 범위로 보정합니다.
func clampScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// scoreToLevel 은 0~100 점수를 risk_level 4단계로 매핑합니다.
// UI 게이지 눈금(33/66)에 맞춰 경계를 잡습니다.
func scoreToLevel(score int) string {
	switch {
	case score >= 67:
		return "high"
	case score >= 34:
		return "medium"
	case score >= 1:
		return "low"
	default:
		return "none"
	}
}

// safetyLabel 은 위험 점수(0~100)를 한국어 안전 라벨로 변환합니다.
// scoreToLevel 을 단일 진실원천으로 경유해 risk_level 과 항상 일치시킵니다.
func safetyLabel(score int) string {
	switch scoreToLevel(score) {
	case "high":
		return "위험"
	case "medium":
		return "주의"
	default: // low, none
		return "안전"
	}
}

// statusLabel 은 위험 점수로 히스토리 상태 라벨을 정합니다.
// medium 이상(score>=34)은 사람 검토가 필요한 needs_review, 그 외는 reviewed.
// scoreToLevel 을 경유해 safetyLabel·통계와 경계를 통일합니다.
func statusLabel(score int) string {
	switch scoreToLevel(score) {
	case "high", "medium":
		return "needs_review"
	default:
		return "reviewed"
	}
}

// normalizeSeverity 는 하이라이트 심각도를 UI 3단계(high|needs_review|low)로 정규화합니다.
// LLM이 medium/주의/경고 등 변형을 줘도 가장 가까운 단계로 흡수합니다.
func normalizeSeverity(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "high", "위험", "danger", "critical":
		return "high"
	case "low", "낮음", "minor":
		return "low"
	default:
		// medium, needs_review, 주의, warning 등은 모두 '주의'로 흡수
		return "needs_review"
	}
}

// phraseOffsets 는 input 의 fromByte 이후에서 phrase 를 찾아 rune(문자) 오프셋
// [start, end) 와 다음 검색 시작 byte 위치(matchEndByte)를 반환합니다.
// 찾지 못하면 ok=false. JS 측 인덱싱과 맞추기 위해 byte가 아닌 rune 단위로 반환합니다.
// fromByte 를 호출부에서 누적하면 같은 phrase 가 여러 번 등장할 때 순차 매칭됩니다.
func phraseOffsets(input, phrase string, fromByte int) (start, end, matchEndByte int, ok bool) {
	if phrase == "" || fromByte < 0 || fromByte > len(input) {
		return 0, 0, 0, false
	}
	rel := strings.Index(input[fromByte:], phrase)
	if rel < 0 {
		return 0, 0, 0, false
	}
	byteIdx := fromByte + rel
	start = utf8.RuneCountInString(input[:byteIdx])
	end = start + utf8.RuneCountInString(phrase)
	matchEndByte = byteIdx + len(phrase)
	return start, end, matchEndByte, true
}

// newReviewID 는 "rev_xxxx" 형태의 검토 ID를 생성합니다(히스토리 PK 겸용).
// 16바이트(128비트) 난수로 충돌 확률을 사실상 0으로 만듭니다.
func newReviewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "rev_00000000000000000000000000000000"
	}
	return "rev_" + hex.EncodeToString(b[:])
}

package rag

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
)

func TestSearchVector_MapsRows(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"id", "title", "category", "event_date", "trigger_expressions", "description", "similarity"}).
		AddRow("e1", "광복절", "HISTORY", "08-15", []byte(`["광복절","해방"]`), "1945년 광복", 0.91).
		AddRow("e2", "세월호", "DISASTER", "04-16", []byte(`["세월호"]`), "2014년 참사", 0.72)
	mock.ExpectQuery("FROM sensitive_events").
		WithArgs(pgxmock.AnyArg(), 5).
		WillReturnRows(rows)

	s := &store{pool: mock}
	got, err := s.searchVector(context.Background(), []float32{0.1, 0.2}, 5)
	if err != nil {
		t.Fatalf("searchVector: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "e1" || got[0].Similarity != 0.91 {
		t.Errorf("row0 = %+v", got[0].Topic)
	}
	if len(got[0].triggers) != 2 || got[0].triggers[0] != "광복절" {
		t.Errorf("triggers = %v", got[0].triggers)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestSearchSimilar_MapsRows(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"id", "title", "category", "snippet", "similarity"}).
		AddRow("i1", "스타벅스 세월호 논란", "DISASTER", "참사일 머그 출시", 0.41).
		AddRow("i2", "다른 사례", "ETC", "설명", 0.33)
	mock.ExpectQuery("FROM sensitive_issues").
		WithArgs(pgxmock.AnyArg(), 5).
		WillReturnRows(rows)

	s := &store{pool: mock}
	got, err := s.searchSimilar(context.Background(), specIssues, []float32{0.1, 0.2}, 5)
	if err != nil {
		t.Fatalf("searchSimilar: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Source != "sensitive_issues" || got[0].ID != "i1" || got[0].Similarity != 0.41 {
		t.Errorf("row0 = %+v", got[0])
	}
	if got[0].Category != "DISASTER" || got[0].Snippet != "참사일 머그 출시" {
		t.Errorf("fields = %+v", got[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestTrendingTerms_MapsRows(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"expr", "category", "up"}).
		AddRow("광복절", "HISTORY", 5).
		AddRow("세월호", "DISASTER", 3)
	mock.ExpectQuery("FROM sensitive_events").
		WithArgs(10).
		WillReturnRows(rows)

	s := &store{pool: mock}
	got, err := s.trendingTerms(context.Background(), 10)
	if err != nil {
		t.Fatalf("trendingTerms: %v", err)
	}
	if len(got) != 2 || got[0].Tag != "#광복절" || got[0].Up != 5 {
		t.Fatalf("got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

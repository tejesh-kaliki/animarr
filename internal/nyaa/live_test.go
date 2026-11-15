package nyaa

import (
	"context"
	"os"
	"testing"
	"time"

	"animarr/internal/indexer"
)

func TestLiveSpiritedAway(t *testing.T) {
	if os.Getenv("ANIMARR_LIVE") != "1" {
		t.Skip("set ANIMARR_LIVE=1 to hit nyaa.si")
	}
	c := New([]string{"https://nyaa.si"}, "1_2", "animarr/0.1 (+homelab; rss sanity)", time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rels, err := c.Search(ctx, indexer.Query{Text: "Spirited Away"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) == 0 {
		t.Fatal("expected at least one result")
	}
	if len(rels[0].InfoHash) != 40 || rels[0].Title == "" {
		t.Fatalf("first result %+v", rels[0])
	}
}

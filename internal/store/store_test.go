package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/faddey/xray-monitor/internal/model"
	"github.com/faddey/xray-monitor/internal/subscription"
)

func TestStoreCheckHistory(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "monitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.SyncNodes(ctx, []subscription.Node{{ID: "n1", Name: "node", Address: "192.0.2.1", Port: 443}}, now); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordChecks(ctx, []model.Check{{NodeID: "n1", CheckedAt: now, Available: true, LatencyMS: 42, StatusCode: 204}}); err != nil {
		t.Fatal(err)
	}
	history, err := db.History(ctx, "n1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || !history[0].Available || history[0].LatencyMS != 42 {
		t.Fatalf("unexpected history: %+v", history)
	}
}

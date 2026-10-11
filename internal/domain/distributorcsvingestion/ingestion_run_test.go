package distributorcsvingestion_test

import (
	"testing"
	"time"

	ingdomain "clinic-inventory-csv-functions/internal/domain/distributorcsvingestion"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

var startedAt = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

func TestNewIngestionRun(t *testing.T) {
	distributorID := shareddomain.NewID()

	tests := []struct {
		name          string
		distributorID shareddomain.ID
		s3Key         string
		etag          string
		wantErr       string
	}{
		{name: "卸業者とS3キーがあれば作れる", distributorID: distributorID, s3Key: "catalogs/oroshi-b/prices.csv", etag: "etag-1"},
		{name: "ETagは空でも作れる", distributorID: distributorID, s3Key: "catalogs/oroshi-b/prices.csv", etag: ""},
		{name: "卸業者が未指定なら作れない", distributorID: shareddomain.ID{}, s3Key: "catalogs/oroshi-b/prices.csv", wantErr: "卸業者の指定は必須です"},
		{name: "S3キーが空なら作れない", distributorID: distributorID, s3Key: "", wantErr: "S3キーは必須です"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, err := ingdomain.NewIngestionRun(tt.distributorID, tt.s3Key, tt.etag, startedAt)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if run.Status() != ingdomain.StatusStaged {
				t.Errorf("作成時の状態 = %s, want staged", run.Status())
			}
			if run.ID().IsZero() {
				t.Error("システム内部IDが発行されていない")
			}
			if run.FinishedAt() != nil {
				t.Error("作成時は終了時刻が無いはず")
			}
		})
	}
}

func newRun(t *testing.T) *ingdomain.IngestionRun {
	t.Helper()
	run, err := ingdomain.NewIngestionRun(shareddomain.NewID(), "catalogs/oroshi-b/prices.csv", "etag-1", startedAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return run
}

func TestIngestionRun_ステージング行の振り分け(t *testing.T) {
	run := newRun(t)
	run.AddRow(2, "B-1001,犬猫用抗生剤,578c…,2480", "B-1001", "犬猫用抗生剤", "卸B取扱", "", "", nil,
		[]ingdomain.StagingFacilityPrice{{FacilityCode: "578c…", UnitPrice: 2480}}, false)
	run.AddInvalidRow(3, ",犬猫用消炎鎮痛剤,578c…,1980", "卸商品コードが取得できません")
	run.AddRow(4, "B-1003,動物用消毒液,578c…,880", "B-1003", "動物用消毒液", "卸B取扱", "", "", nil,
		[]ingdomain.StagingFacilityPrice{{FacilityCode: "578c…", UnitPrice: 880}}, false)

	if len(run.Rows()) != 3 {
		t.Fatalf("len(Rows) = %d, want 3（不備のある行も含めてすべて持つ）", len(run.Rows()))
	}

	valid := run.ValidRows()
	if len(valid) != 2 || valid[0].RowNo() != 2 || valid[1].RowNo() != 4 {
		t.Errorf("反映の対象は2行目と4行目のはず: %+v", valid)
	}

	invalid := run.InvalidRows()
	if len(invalid) != 1 {
		t.Fatalf("len(InvalidRows) = %d, want 1", len(invalid))
	}
	if invalid[0].RowNo() != 3 || invalid[0].Valid() {
		t.Errorf("不備のある行は3行目で、反映の対象外のはず: %+v", invalid[0])
	}
	// 不備のある行は、理由と原文を必ず持つ
	if invalid[0].ErrorMessage() != "卸商品コードが取得できません" || invalid[0].Raw() == "" {
		t.Errorf("理由と原文が残っていない: msg=%q raw=%q", invalid[0].ErrorMessage(), invalid[0].Raw())
	}
}

func TestIngestionRun_状態の移り変わり(t *testing.T) {
	finishedAt := startedAt.Add(time.Minute)

	t.Run("要確認にすると、理由と終了時刻が残る", func(t *testing.T) {
		run := newRun(t)
		run.MarkNeedsReview("2行に不備があります", finishedAt)
		if run.Status() != ingdomain.StatusNeedsReview {
			t.Errorf("状態 = %s, want needs_review", run.Status())
		}
		if run.Message() != "2行に不備があります" {
			t.Errorf("理由 = %q", run.Message())
		}
		if run.FinishedAt() == nil || !run.FinishedAt().Equal(finishedAt) {
			t.Errorf("終了時刻 = %v, want %v", run.FinishedAt(), finishedAt)
		}
	})

	t.Run("反映済みにすると、終了時刻が残り、理由は空になる", func(t *testing.T) {
		run := newRun(t)
		run.MarkNeedsReview("前の理由", finishedAt)
		run.MarkApplied(finishedAt)
		if run.Status() != ingdomain.StatusApplied {
			t.Errorf("状態 = %s, want applied", run.Status())
		}
		if run.Message() != "" {
			t.Errorf("反映済みなら理由は空のはず: %q", run.Message())
		}
		if run.FinishedAt() == nil {
			t.Error("終了時刻が無い")
		}
	})
}

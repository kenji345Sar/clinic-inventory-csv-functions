package distributorcsvingestion

import (
	"context"
	"encoding/json"
	"fmt"

	ingdomain "clinic-inventory-csv-functions/internal/domain/distributorcsvingestion"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IngestionRunRepository struct {
	db *gorm.DB
}

func NewIngestionRunRepository(db *gorm.DB) *IngestionRunRepository {
	return &IngestionRunRepository{db: db}
}

func (r *IngestionRunRepository) IsAlreadyApplied(ctx context.Context, s3Key, etag string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&IngestionRunModel{}).
		Where("s3_key = ? AND etag = ? AND status = ?", s3Key, etag, string(ingdomain.StatusApplied)).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Save は取り込み実行とステージング行をトランザクション内でまとめて保存する。
// 同じ実行を「ステージング済み」→「反映済み」と2回保存するため、実行はupsert、
// 行は全削除してから入れ直す（発注の明細と同じ方針）。
func (r *IngestionRunRepository) Save(ctx context.Context, run *ingdomain.IngestionRun) error {
	runModel, rowModels, err := toIngestionModels(run)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"status", "message", "finished_at"}),
		}).Create(&runModel).Error; err != nil {
			return err
		}
		if err := tx.Where("ingestion_run_id = ?", runModel.ID).
			Delete(&IngestionStagingRowModel{}).Error; err != nil {
			return err
		}
		if len(rowModels) > 0 {
			if err := tx.Create(&rowModels).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *IngestionRunRepository) FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*ingdomain.IngestionRun, error) {
	var runModels []IngestionRunModel
	if err := r.db.WithContext(ctx).
		Where("distributor_id = ?", uuid.UUID(distributorID)).
		Order("started_at DESC").
		Find(&runModels).Error; err != nil {
		return nil, err
	}

	runs := make([]*ingdomain.IngestionRun, 0, len(runModels))
	for _, runModel := range runModels {
		var rowModels []IngestionStagingRowModel
		if err := r.db.WithContext(ctx).
			Where("ingestion_run_id = ?", runModel.ID).
			Order("row_no").
			Find(&rowModels).Error; err != nil {
			return nil, err
		}
		run, err := toDomainIngestionRun(runModel, rowModels)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func toIngestionModels(run *ingdomain.IngestionRun) (IngestionRunModel, []IngestionStagingRowModel, error) {
	runModel := IngestionRunModel{
		ID:            uuid.UUID(run.ID()),
		DistributorID: uuid.UUID(run.DistributorID()),
		S3Key:         run.S3Key(),
		ETag:          run.ETag(),
		Status:        string(run.Status()),
		Message:       run.Message(),
		StartedAt:     run.StartedAt(),
		FinishedAt:    run.FinishedAt(),
	}

	rows := run.Rows()
	rowModels := make([]IngestionStagingRowModel, 0, len(rows))
	for _, row := range rows {
		facilityPrices := ""
		if len(row.FacilityPrices()) > 0 {
			encoded, err := json.Marshal(row.FacilityPrices())
			if err != nil {
				return IngestionRunModel{}, nil, fmt.Errorf("医院別単価の保存形式への変換に失敗しました(%d行目): %w", row.RowNo(), err)
			}
			facilityPrices = string(encoded)
		}
		rowModels = append(rowModels, IngestionStagingRowModel{
			IngestionRunID:         runModel.ID,
			RowNo:                  row.RowNo(),
			Raw:                    row.Raw(),
			Valid:                  row.Valid(),
			ErrorMessage:           row.ErrorMessage(),
			DistributorProductCode: row.DistributorProductCode(),
			Name:                   row.Name(),
			VendorName:             row.VendorName(),
			VendorProductCode:      row.VendorProductCode(),
			JANCode:                row.JANCode(),
			UnitPrice:              row.UnitPrice(),
			FacilityPrices:         facilityPrices,
			Discontinued:           row.Discontinued(),
		})
	}
	return runModel, rowModels, nil
}

func toDomainIngestionRun(runModel IngestionRunModel, rowModels []IngestionStagingRowModel) (*ingdomain.IngestionRun, error) {
	rows := make([]ingdomain.StagingRow, 0, len(rowModels))
	for _, rowModel := range rowModels {
		var facilityPrices []ingdomain.StagingFacilityPrice
		if rowModel.FacilityPrices != "" {
			if err := json.Unmarshal([]byte(rowModel.FacilityPrices), &facilityPrices); err != nil {
				return nil, fmt.Errorf("医院別単価の読み出しに失敗しました(%d行目): %w", rowModel.RowNo, err)
			}
		}
		rows = append(rows, ingdomain.ReconstructStagingRow(
			rowModel.RowNo,
			rowModel.Raw,
			rowModel.Valid,
			rowModel.ErrorMessage,
			rowModel.DistributorProductCode,
			rowModel.Name,
			rowModel.VendorName,
			rowModel.VendorProductCode,
			rowModel.JANCode,
			rowModel.UnitPrice,
			facilityPrices,
			rowModel.Discontinued,
		))
	}
	return ingdomain.ReconstructIngestionRun(
		shareddomain.ID(runModel.ID),
		shareddomain.ID(runModel.DistributorID),
		runModel.S3Key,
		runModel.ETag,
		ingdomain.Status(runModel.Status),
		runModel.Message,
		runModel.StartedAt,
		runModel.FinishedAt,
		rows,
	), nil
}

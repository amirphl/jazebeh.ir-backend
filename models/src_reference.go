package models

// SrcReference mirrors the schema used by src_reference.sql. ID corresponds
// to tags.id, but the database intentionally does not enforce a foreign key so
// the supplied data dump can be imported unchanged.
type SrcReference struct {
	ID             uint    `gorm:"column:id;primaryKey"   json:"id"`
	SrcAddress     *string `gorm:"column:src_address"     json:"src_address,omitempty"`
	Layer1Category *string `gorm:"column:layer1_category" json:"layer1_category,omitempty"`
	Layer2Category *string `gorm:"column:layer2_category" json:"layer2_category,omitempty"`
	Layer3Category *string `gorm:"column:layer3_category" json:"layer3_category,omitempty"`
	TagCount       *int64  `gorm:"column:tag_count"       json:"tag_count,omitempty"`
}

func (SrcReference) TableName() string {
	return "src_reference"
}

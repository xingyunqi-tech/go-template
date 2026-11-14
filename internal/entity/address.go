package entity

import "time"

// Address is a shipping address of a user.
type Address struct {
	ID           int
	UserID       int
	Name         string
	Phone        string
	ProvinceCode *string
	CityCode     *string
	DistrictCode *string
	Province     string
	City         string
	District     string
	Address      string
	IsDefault    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

// Region levels.
const (
	RegionProvince = 1
	RegionCity     = 2
	RegionDistrict = 3
)

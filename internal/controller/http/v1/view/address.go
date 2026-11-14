package view

import (
	"encoding/json"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

type addressJSON struct {
	ID           int            `json:"id"`
	UserID       int            `json:"userId"`
	Name         string         `json:"name"`
	Phone        string         `json:"phone"`
	ProvinceCode *string        `json:"provinceCode"`
	CityCode     *string        `json:"cityCode"`
	DistrictCode *string        `json:"districtCode"`
	Province     string         `json:"province"`
	City         string         `json:"city"`
	District     string         `json:"district"`
	Address      string         `json:"address"`
	IsDefault    bool           `json:"isDefault"`
	CreatedAt    jsontime.Time  `json:"createdAt"`
	UpdatedAt    jsontime.Time  `json:"updatedAt"`
	DeletedAt    *jsontime.Time `json:"deletedAt"`
}

// Address is the nestjs Address in AddressMapper key order; the zero Address is written as null.
type Address struct {
	data *addressJSON
}

// MarshalJSON implements json.Marshaler.
func (a Address) MarshalJSON() ([]byte, error) {
	if a.data == nil {
		return []byte("null"), nil
	}

	return json.Marshal(a.data) //nolint:wrapcheck // plain values
}

// NewAddress returns the view of a; nil is written as null.
func NewAddress(a *entity.Address) Address {
	if a == nil {
		return Address{data: nil}
	}

	return Address{data: &addressJSON{
		ID: a.ID, UserID: a.UserID, Name: a.Name, Phone: a.Phone, ProvinceCode: a.ProvinceCode,
		CityCode: a.CityCode, DistrictCode: a.DistrictCode, Province: a.Province, City: a.City,
		District: a.District, Address: a.Address, IsDefault: a.IsDefault,
		CreatedAt: jsontime.New(a.CreatedAt), UpdatedAt: jsontime.New(a.UpdatedAt), DeletedAt: timePtr(a.DeletedAt),
	}}
}

// AddressPage is the address list response.
type AddressPage = pagination.Page[Address]

package view

import (
	"github.com/xingyunqi-tech/go-template/internal/usecase/region"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

// Region is {id, code, name, level, parentCode, sort, createdAt, updatedAt}.
type Region struct {
	ID         int           `json:"id"`
	Code       string        `json:"code"`
	Name       string        `json:"name"`
	Level      int           `json:"level"`
	ParentCode *string       `json:"parentCode"`
	Sort       int           `json:"sort"`
	CreatedAt  jsontime.Time `json:"createdAt"`
	UpdatedAt  jsontime.Time `json:"updatedAt"`
}

// NewRegion returns the view of r.
func NewRegion(r *region.Region) Region {
	return Region{
		ID: r.ID, Code: r.Code, Name: r.Name, Level: r.Level, ParentCode: r.ParentCode, Sort: r.Sort,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// NewRegions returns the views of rs; an empty list is [].
func NewRegions(rs []region.Region) []Region {
	out := make([]Region, len(rs))
	for i := range rs {
		out[i] = NewRegion(&rs[i])
	}

	return out
}

// RegionTree is a Region followed by its children; leaves have [].
type RegionTree struct {
	Region

	Children []RegionTree `json:"children"`
}

// NewRegionTree returns the views of nodes.
func NewRegionTree(nodes []*region.Node) []RegionTree {
	out := make([]RegionTree, len(nodes))
	for i, n := range nodes {
		out[i] = RegionTree{Region: NewRegion(&n.Region), Children: NewRegionTree(n.Children)}
	}

	return out
}

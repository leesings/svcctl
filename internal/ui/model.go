package ui

import (
	"fmt"

	"github.com/lxn/walk"

	"svcctl/internal/core"
)

// serviceModel 为 TableView 提供服务列表数据。
type serviceModel struct {
	walk.TableModelBase
	items []*core.Service
}

func newServiceModel(items []*core.Service) *serviceModel {
	return &serviceModel{items: items}
}

func (m *serviceModel) RowCount() int {
	return len(m.items)
}

func (m *serviceModel) Value(row, col int) interface{} {
	s := m.at(row)
	if s == nil {
		return ""
	}

	switch col {
	case 0:
		return s.Name
	case 1:
		return s.ID
	case 2:
		return s.Status
	case 3:
		if s.PID > 0 {
			return fmt.Sprintf("%d", s.PID)
		}
		return "-"
	case 4:
		if s.AutoRestart {
			return "是"
		}
		return "否"
	case 5:
		return s.CreatedAt.Format("2006-01-02 15:04:05")
	case 6:
		return s.ExePath
	}
	return ""
}

func (m *serviceModel) at(row int) *core.Service {
	if row < 0 || row >= len(m.items) {
		return nil
	}
	return m.items[row]
}

func (m *serviceModel) reset(items []*core.Service) {
	m.items = items
	m.PublishRowsReset()
}

// sync 在服务集合不变时原地刷新，避免重建列表丢失选中项。
func (m *serviceModel) sync(items []*core.Service) {
	if len(m.items) != len(items) {
		m.reset(items)
		return
	}
	for i := range items {
		if m.items[i].ID != items[i].ID {
			m.reset(items)
			return
		}
	}

	m.items = items
	if len(items) > 0 {
		m.PublishRowsChanged(0, len(items)-1)
	}
}

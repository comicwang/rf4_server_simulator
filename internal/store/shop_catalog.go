package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// shop_kind_defs.json 保存商店定义与 7 类基础货架数据。它只提供默认货架，
// 不替代玩家库中的门店货架或 shop_item_override 覆盖数据。
//
//go:embed assets/shop_kind_defs.json
var embeddedShopKindDefs []byte

// shop_houses.json 保存房产商店（housestore_*）的实抓货架。kind 7 没有真实
// 见证记录，目录模板误用了 farmstore 的农场货架，客户端打开买房界面拉取
// 22/1 时看不到房子商品，无法发起购买。房价与 22/5 面板（gameserver 侧
// shop_boats.json）保持一致，避免展示价与结算价不一致。
//
//go:embed assets/shop_houses.json
var embeddedShopHouses []byte

type shopCatalogSKU struct {
	ItemID string `json:"item_id"`
	Silver int64  `json:"silver"`
	Gold   int64  `json:"gold"`
	Stock  int64  `json:"stock"`
}

type shopStoreDefinition struct {
	Kind   int              `json:"kind"`
	Brands []string         `json:"brands"`
	MapKey string           `json:"map_key"`
	Books  []string         `json:"books"`
	Shelf  []shopCatalogSKU `json:"shelf,omitempty"`
}

type shopKindCatalog struct {
	Stores  map[string]shopStoreDefinition `json:"stores"`
	Kinds   map[string][]shopCatalogSKU    `json:"kinds"`
	Sources map[string]string              `json:"sources"`
}

type shopHouseRow struct {
	StoreID string `json:"store_id"`
	ItemID  string `json:"item_id"`
	Silver  int64  `json:"silver"`
	Gold    int64  `json:"gold"`
}

var (
	shopCatalogOnce sync.Once
	shopCatalogData *shopKindCatalog
	shopCatalogErr  error

	shopHousesOnce sync.Once
	shopHouseErr   error
	shopHouseShelf map[string][]*ShopPrice
)

func embeddedShopKindCatalog() (*shopKindCatalog, error) {
	shopCatalogOnce.Do(func() {
		var catalog shopKindCatalog
		if err := json.Unmarshal(embeddedShopKindDefs, &catalog); err != nil {
			shopCatalogErr = fmt.Errorf("解析内嵌商店目录: %w", err)
			return
		}
		if len(catalog.Stores) == 0 {
			shopCatalogErr = fmt.Errorf("内嵌商店目录没有 stores")
			return
		}
		shopCatalogData = &catalog
	})
	return shopCatalogData, shopCatalogErr
}

// embeddedShopHouseShelf 返回房产商店货架（store_id → 单行房子商品）。
func embeddedShopHouseShelf() (map[string][]*ShopPrice, error) {
	shopHousesOnce.Do(func() {
		var payload struct {
			Rows []shopHouseRow `json:"rows"`
		}
		if err := json.Unmarshal(embeddedShopHouses, &payload); err != nil {
			shopHouseErr = fmt.Errorf("解析内嵌房产货架: %w", err)
			return
		}
		shopHouseShelf = make(map[string][]*ShopPrice, len(payload.Rows))
		for _, row := range payload.Rows {
			if row.StoreID == "" || row.ItemID == "" {
				continue
			}
			shopHouseShelf[row.StoreID] = append(shopHouseShelf[row.StoreID], &ShopPrice{
				ItemID: row.ItemID,
				Silver: row.Silver,
				Gold:   row.Gold,
				Stock:  9999,
			})
		}
	})
	return shopHouseShelf, shopHouseErr
}

type shopOverrideValue struct {
	Silver sql.NullInt64
	Gold   sql.NullInt64
	Stock  sql.NullInt64
	// Listed=1 表示后台强制上架：即使商品不在基础货架也挂到生效货架。
	Listed sql.NullInt64
}

const (
	shopShelfSourceRecorded = "recorded"
	shopShelfSourceStore    = "store_definition"
	shopShelfSourceBrand    = "brand_reuse"
	shopShelfSourceKind     = "kind_template"
	shopShelfSourceMissing  = "missing_evidence"
	shopShelfSourceUnknown  = "unknown_store"
	shopShelfSourceHouse    = "house_store"
)

type shopShelfResolution struct {
	Base         []*ShopPrice
	Effective    []*ShopPrice
	Overrides    map[string]shopOverrideValue
	Definition   shopStoreDefinition
	CatalogKnown bool
	Source       string
}

func (d *DB) loadRecordedShopShelves(ctx context.Context) (map[string][]*ShopPrice, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT store_id, item_id, silver, gold, stock
		FROM shop_item ORDER BY store_id, item_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]*ShopPrice)
	for rows.Next() {
		var storeID string
		p := &ShopPrice{}
		if err := rows.Scan(&storeID, &p.ItemID, &p.Silver, &p.Gold, &p.Stock); err != nil {
			return nil, err
		}
		out[storeID] = append(out[storeID], p)
	}
	return out, rows.Err()
}

func (d *DB) loadShopOverrides(ctx context.Context, storeID string) (map[string]shopOverrideValue, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT item_id, silver, gold, stock, COALESCE(listed,0)
		FROM shop_item_override WHERE store_id=?`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]shopOverrideValue)
	for rows.Next() {
		var itemID string
		var v shopOverrideValue
		if err := rows.Scan(&itemID, &v.Silver, &v.Gold, &v.Stock, &v.Listed); err != nil {
			return nil, err
		}
		out[itemID] = v
	}
	return out, rows.Err()
}

func (d *DB) loadAllShopOverrides(ctx context.Context) (map[string]map[string]shopOverrideValue, error) {
	rows, err := d.SQL.QueryContext(ctx, `
		SELECT store_id, item_id, silver, gold, stock, COALESCE(listed,0) FROM shop_item_override`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]map[string]shopOverrideValue)
	for rows.Next() {
		var storeID, itemID string
		var v shopOverrideValue
		if err := rows.Scan(&storeID, &itemID, &v.Silver, &v.Gold, &v.Stock, &v.Listed); err != nil {
			return nil, err
		}
		m := out[storeID]
		if m == nil {
			m = make(map[string]shopOverrideValue)
			out[storeID] = m
		}
		m[itemID] = v
	}
	return out, rows.Err()
}

func shopBrandKey(brands []string) string {
	if len(brands) == 0 {
		return ""
	}
	cp := append([]string(nil), brands...)
	sort.Strings(cp)
	return strings.Join(cp, "\x00")
}

func copyShopPrices(rows []*ShopPrice) []*ShopPrice {
	out := make([]*ShopPrice, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		v := *row
		out = append(out, &v)
	}
	return out
}

func catalogShopPrices(rows []shopCatalogSKU) []*ShopPrice {
	out := make([]*ShopPrice, 0, len(rows))
	for _, row := range rows {
		out = append(out, &ShopPrice{
			ItemID: row.ItemID,
			Silver: row.Silver,
			Gold:   row.Gold,
			Stock:  row.Stock,
		})
	}
	return out
}

// resolveShopBase 优先保留逐店实录及其价格，仅用明确登记的逐店目录补齐缺失商品。
// 没有逐店实录时才用逐店目录兜底，再依次尝试同类型同品牌实录和同类型模板。
// 返回的货架不与实录或内嵌目录共享可变行，门店覆盖在选择基础货架后应用。
//
// resolveShopBase preserves exact recordings and their prices, adding only missing
// items explicitly registered in the per-store definition. Without exact recordings,
// it tries per-store definitions, same-kind/brand recordings, then kind templates.
// Returned rows do not alias either source; overrides are applied afterward.
func resolveShopBase(storeID string, catalog *shopKindCatalog, recorded map[string][]*ShopPrice) ([]*ShopPrice, string, shopStoreDefinition, bool) {
	def, known := catalog.Stores[storeID]
	if rows := recorded[storeID]; len(rows) > 0 {
		base := copyShopPrices(rows)
		if len(def.Shelf) > 0 {
			have := make(map[string]bool, len(base)+len(def.Shelf))
			for _, row := range base {
				have[row.ItemID] = true
			}
			for _, row := range catalogShopPrices(def.Shelf) {
				if !have[row.ItemID] {
					base = append(base, row)
					have[row.ItemID] = true
				}
			}
		}
		return base, shopShelfSourceRecorded, def, known
	}
	if !known {
		return nil, shopShelfSourceUnknown, def, false
	}
	if len(def.Shelf) > 0 {
		return catalogShopPrices(def.Shelf), shopShelfSourceStore, def, true
	}

	kindKey := strconv.Itoa(def.Kind)
	brandKey := shopBrandKey(def.Brands)

	// Unbranded stores are a single franchise family. Prefer the explicitly
	// documented witness store for this kind when it has a recording.
	if len(def.Brands) == 0 {
		if source := catalog.Sources[kindKey]; source != "" {
			if rows := recorded[source]; len(rows) > 0 {
				return copyShopPrices(rows), shopShelfSourceBrand, def, true
			}
		}
	}

	recordedStores := make([]string, 0, len(recorded))
	for sid := range recorded {
		recordedStores = append(recordedStores, sid)
	}
	sort.Strings(recordedStores)
	for _, sid := range recordedStores {
		other, ok := catalog.Stores[sid]
		if !ok || other.Kind != def.Kind {
			continue
		}
		if shopBrandKey(other.Brands) != brandKey {
			continue
		}
		if rows := recorded[sid]; len(rows) > 0 {
			return copyShopPrices(rows), shopShelfSourceBrand, def, true
		}
	}

	if rows := catalog.Kinds[kindKey]; len(rows) > 0 {
		return catalogShopPrices(rows), shopShelfSourceKind, def, true
	}
	return nil, shopShelfSourceMissing, def, true
}

func applyShopOverrides(base []*ShopPrice, overrides map[string]shopOverrideValue) []*ShopPrice {
	out := make([]*ShopPrice, 0, len(base))
	for _, row := range base {
		if row == nil {
			continue
		}
		v := *row
		if override, ok := overrides[v.ItemID]; ok {
			if override.Silver.Valid {
				v.Silver = override.Silver.Int64
			}
			if override.Gold.Valid {
				v.Gold = override.Gold.Int64
			}
			if override.Stock.Valid {
				v.Stock = override.Stock.Int64
			}
		}
		// 本地商店持续供货，零库存自动补回基础批量。
		if v.Stock <= 0 {
			v.Stock = 1000
		}
		out = append(out, &v)
	}
	return out
}

// appendListedShopItems 把后台强制上架（listed=1）的覆盖商品追加到基础货架，
// 顺序按 item_id 稳定排序，保证货架输出确定。价格优先取覆盖值；银/金币都
// 未填时回退全局 item_price；库存未填时由 applyShopOverrides 的持续供货
// 规则补到 1000。目录里已不存在（或从未存在）的商品跳过，避免客户端拿到
// 无法渲染的货架行。
func (d *DB) appendListedShopItems(ctx context.Context, base []*ShopPrice, overrides map[string]shopOverrideValue) ([]*ShopPrice, error) {
	listed := make([]string, 0)
	have := make(map[string]bool, len(base))
	for _, row := range base {
		have[row.ItemID] = true
	}
	for itemID, ov := range overrides {
		if !ov.Listed.Valid || ov.Listed.Int64 != 1 || have[itemID] {
			continue
		}
		listed = append(listed, itemID)
	}
	if len(listed) == 0 {
		return base, nil
	}
	sort.Strings(listed)
	for _, itemID := range listed {
		var exists int
		if err := d.SQL.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM item_def WHERE item_id=?`, itemID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			continue
		}
		ov := overrides[itemID]
		row := &ShopPrice{ItemID: itemID}
		if ov.Silver.Valid {
			row.Silver = ov.Silver.Int64
		}
		if ov.Gold.Valid {
			row.Gold = ov.Gold.Int64
		}
		if ov.Stock.Valid {
			row.Stock = ov.Stock.Int64
		}
		if !ov.Silver.Valid && !ov.Gold.Valid {
			if p, err := d.ItemPriceGet(ctx, itemID); err == nil {
				row.Silver, row.Gold, row.Stock = p.Silver, p.Gold, p.Stock
			}
		}
		base = append(base, row)
		have[itemID] = true
	}
	return base, nil
}

func (d *DB) effectiveShopShelf(ctx context.Context, storeID string) (*shopShelfResolution, error) {
	storeID = strings.TrimSpace(storeID)
	catalog, err := embeddedShopKindCatalog()
	if err != nil {
		return nil, err
	}
	// 房产商店走实抓货架：kind 7 模板是误用的农场货架，补齐逻辑还会把
	// 同类目的其他房子估价塞进来，都不该出现在买房界面。
	houses, houseErr := embeddedShopHouseShelf()
	if houseErr != nil {
		return nil, houseErr
	}
	if rows, ok := houses[storeID]; ok {
		base := copyShopPrices(rows)
		overrides, err := d.loadShopOverrides(ctx, storeID)
		if err != nil {
			return nil, err
		}
		effective := applyShopOverrides(base, overrides)
		effective, err = d.appendListedShopItems(ctx, effective, overrides)
		if err != nil {
			return nil, err
		}
		def := catalog.Stores[storeID]
		return &shopShelfResolution{
			Base:         base,
			Effective:    effective,
			Overrides:    overrides,
			Definition:   def,
			CatalogKnown: true,
			Source:       shopShelfSourceHouse,
		}, nil
	}
	recorded, err := d.loadRecordedShopShelves(ctx)
	if err != nil {
		return nil, err
	}
	base, source, def, known := resolveShopBase(storeID, catalog, recorded)
	base, _, err = d.augmentShopShelf(ctx, def, base)
	if err != nil {
		return nil, err
	}
	overrides, err := d.loadShopOverrides(ctx, storeID)
	if err != nil {
		return nil, err
	}
	effective := applyShopOverrides(base, overrides)
	// 补齐货架：历史抓取/内嵌模板缺的新商品（例如 2025 款）在客户端看来是
	// “无库存”，这里按“同店内已有品类 +（品牌店）同品牌”补上，价格缺失时
	// 用同类商品中位价估价。
	effective, _, err = d.augmentShopShelf(ctx, def, effective)
	if err != nil {
		return nil, err
	}
	// 后台强制上架最后追加：避免 augment 把上架商品的整个品类按补齐规则
	// 扩散进货架（在饵店挂一件食品，不应连带把所有食品都拉上架）。
	effective, err = d.appendListedShopItems(ctx, effective, overrides)
	if err != nil {
		return nil, err
	}
	return &shopShelfResolution{
		Base:         base,
		Effective:    effective,
		Overrides:    overrides,
		Definition:   def,
		CatalogKnown: known,
		Source:       source,
	}, nil
}

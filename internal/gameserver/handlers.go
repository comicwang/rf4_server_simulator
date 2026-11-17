package gameserver

import (
	"encoding/json"
	"fmt"

	"rf4server/internal/proto"
)

// RegisterExtra 添加处理器注册函数，供各功能模块在初始化时注册命令。
var extraRegistrations []func(*Dispatch)

func RegisterExtra(f func(*Dispatch)) { extraRegistrations = append(extraRegistrations, f) }

// registerHandlers 注册内置命令处理器。
func registerHandlers(d *Dispatch) {
	for _, f := range extraRegistrations {
		f(d)
	}
	d.Register(1, 1, hGameVersion)
	d.Register(1, 2, hGameTime)
	d.Register(1, 4, hGameConfigs)
	d.Register(1, 5, hGameConfigsVersion)
	registerSessionHandlers(d)
	registerShopHandlers(d)
	registerMailHandlers(d)
	registerTimelineHandlers(d)
	registerRigHandlers(d) // 15/16/17 竿组装配命令
	registerWorldHandlers(d)
	registerTradingCardHandlers(d)
	// 动态协议、档案、社交和竞赛处理器覆盖静态兜底，需在基础处理器之后注册。
	registerDynamicProtocolHandlers(d)
	registerAchievementHandlers(d)
	registerSocialHandlers(d)
	registerCompetitionHandlers(d)
}

// AcceptedVersion 是服务端接受的客户端版本。
const AcceptedVersion = "4.0.25034"

// hGameVersion 1/1：回可接受版本串（marker 0x14 短字符串）。
func hGameVersion(ctx *Ctx) ([]byte, bool) {
	body := make([]byte, 0, 2+len(AcceptedVersion))
	body = append(body, 0x14, byte(len(AcceptedVersion)))
	body = append(body, AcceptedVersion...)
	return body, true
}

// hGameTime 1/2：回当前时刻 FILETIME（0x11 + u64）。
// 客户端把它 ×24 展开成游戏时间——必须回真实当前时刻。
func hGameTime(ctx *Ctx) ([]byte, bool) {
	body := append([]byte{0x11}, proto.PackU64(uint64(proto.WindowsFiletimeNow()))...)
	return body, true
}

// hGameConfigs 1/4：按请求类型回纯 LZMA 配置 blob（0x08 bytes 包裹）。
// 使用与 1/5 相同的会话快照，避免管理端更新期间版本和内容不一致。
func hGameConfigs(ctx *Ctx) ([]byte, bool) {
	typ, ok := ctx.ArgInt(0)
	if !ok || typ == 0 {
		return emptyArgsBody(), true
	}
	if typ > 9 {
		return emptyArgsBody(), true
	}
	configs, err := ctx.Sess.clientConfigs()
	if err != nil {
		ctx.Log.Error("1/4 构建客户端配置失败", "err", err)
		return nil, false
	}
	blob := configs.blobs[typ-1]
	ctx.Log.Info("1/4 下发配置", "type", typ, "bytes", len(blob))
	body := append([]byte{0x08}, proto.PackU32(uint32(len(blob)))...)
	return append(body, blob...), true
}

// hGameConfigsVersion 1/5：返回 9 个 SHA1 字符串的 0x04 数组。
// 响应不能为空数组，客户端启动流程会直接读取首项。
func hGameConfigsVersion(ctx *Ctx) ([]byte, bool) {
	configs, err := ctx.Sess.clientConfigs()
	if err != nil {
		ctx.Log.Error("1/5 构建客户端配置失败", "err", err)
		return nil, false
	}
	vers := configs.versions
	out := []byte{0x04}
	out = append(out, proto.PackU16(uint32(len(vers)))...)
	for _, v := range vers {
		out = append(out, proto.PackU32(uint32(len(v)))...)
		out = append(out, v...)
	}
	return out, true
}

// ---- config_wire 资产 ----

func configBlob(typ int) ([]byte, error) {
	if typ < 1 || typ > 9 {
		return nil, fmt.Errorf("类型越界 %d", typ)
	}
	name := fmt.Sprintf("assets/config_wire/type_%d.bin", typ)
	return configWireFS.ReadFile(name)
}

func configVersions() ([][]byte, error) {
	raw, err := configWireFS.ReadFile("assets/config_wire/index.json")
	if err != nil {
		return nil, err
	}
	// types 含有额外元数据；这里只读取 versions，其余字段跳过。
	var idx struct {
		Versions []string        `json:"versions"`
		Types    json.RawMessage `json:"types"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(idx.Versions))
	for _, v := range idx.Versions {
		out = append(out, []byte(v))
	}
	return out, nil
}

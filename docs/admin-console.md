# 管理控制台

管理控制台直接内嵌在 `launcher` 中，通过统一控制中心 `http://127.0.0.1:9500` 的“管理后台”页签进入，不再监听独立的 `8080` 端口。admin 的 HTTP handler 挂载在 `/admin/`，前端 API 使用 `/admin/api/*`；内部 handler 仍保持 `/api/*` 路由，便于测试和独立调试。控制台使用 `config.yaml` 中的管理员凭据进行认证；登录成功后返回 JWT，供控制台和 API 使用。

该界面可以修改玩家数据、发放物品、调整经济配置以及管理会话。本地入口用于可信网络；独立 `cmd/admin` 可配置 HTTPS，正式环境入口与证书安装见 [正式环境客户端配置](production-client.md)。

## 玩家管理

- 按 UID 或名称搜索玩家。
- 新建玩家：UID 留空自动分配（当前最大 UID+1，不低于 1000001），可指定昵称、邮箱、等级、经验、银币、金币和银行存款；缺省用新档默认值（昵称 player · Lv.1 · 银币 5000）。新建档案不会自动登录；需在玩家详情设置个人中心账号，并通过 `/login.php` 的账号密码认证绑定角色。Steam ticket 暂不支持本地验证，不再匿名分配固定角色。
- 编辑等级、经验、银币、金币和银行存款。
- 查看并修改背包和鱼护内容。
- 添加或移除鱼。
- 编辑各钓鱼技能分类的熟练度。
- 发放目录中的物品并移除已有物品。
- 将在线玩家踢下线或封禁玩家。
- 在玩家档案设置唯一的个人中心账号和密码；重置密码或封禁玩家会撤销已登录的个人中心会话。
- 设置账号时同时生成 RF4 客户端摘要的 bcrypt 校验值，游戏使用同一账号密码。更新前的旧账号可通过重新登录网页个人中心补齐，无需更换密码。

影响在线客户端的修改会通过 gameserver 命令队列下发。

手竿与博洛尼亚、纺车竿与枪柄竿分别共享技能等级，每组只计算一份投入；学习或重置任一项会同步整组，各项熟练度门槛仍独立。历史节点按组内最高等级读取，不批量改写旧记录，也不调整等级赠点和已有额外点数。

## 商店

- 基础商店目录为只读数据。
- 按商店和物品设置的覆盖价格与库存会单独存储。
- 货架按商品类别与关键词筛选；上架选择器也可按目录类别和关键词查找。
- 商品行、上架结果和钓场鱼种显示本机客户端素材。素材不随源码或发布包分发，缺失时显示占位；从客户端 `resources.assets` 导出本地图片：

```text
python -m pip install UnityPy==1.23.0
python tools/extract_catalog_media.py --client-data 'D:/Games/rf4game/Game/rf4_x64_Data' --out 'D:/rf4/ref/catalog_media'
```

- `ref/catalog_media/manifest.json` 列出完整商品与鱼类目录的素材覆盖和缺失 ID。2026-11-17 本机客户端导出结果：商品 11,051/19,388，鱼卡 253/253；没有客户端对应贴图的商品显示「暂无」。
- 价格和库存可以恢复为基础目录中的值。
- 定制商品：从完整目录复制为独立商品 ID，编辑定义中已有的数值属性，包括嵌套部件的重量、曳力等。商品行「编辑属性」可继续修改已上架的定制商品，无需重新上架。客户端定义只更换数字 ID 和实际编辑的数值，其余参数、组件、数组和标志完整继承原商品；已创建的定制商品同样使用此逻辑。创建或修改后完全退出游戏并重新登录，下载更新的商品配置。
- 客户端以商品名称键建立唯一索引，定制商品必须使用独立键；复用原商品键会在登录后触发客户端启动异常。后台识别名用于管理端目录；原版客户端没有新键对应的本地化与贴图，游戏内新名称和缩略图需要客户端资源支持。
- 上架新商品：从完整目录选择商品，可一次勾选多个商店并批量上架；价格库存按统一规则处理，任一商店无效则整批回滚。两个价格都留空时按全局 `item_price` 自动补价，库存留空按持续供货口径 1000。已上架商品带「上架」标记，删除覆盖即下架。
- 按商品统一更新：点击「按商品统一更新」或商品行的「全店更新」，选择商品并预览所有售卖商店，统一设置银价、金币价和库存。提交在一个事务中完成，保留各店原上架状态；基础货架、自动补全货架与显式上架都计入范围。价格单位为分，明确填写 0 会保留零价；重新打开游戏商店获取最新价格和库存。
- 控制台、商店浏览和购买结算使用同一套目录解析逻辑。

解析顺序和覆盖范围详见 [shop_catalog.md](shop_catalog.md)。

## 钓鱼配置

- 选择水域并配置咬钩概率和时间。
- 启用或禁用鱼种，并设置相对权重。
- 从完整鱼池中添加鱼种。
- 在鱼种行查看爱吃的饵摘要，并直接编辑吃/不吃/底钓只吃规则与饵料轮换；也可在「鱼饵偏好」页跨水域筛选和批量查看。
- 将水域配置重置为静态默认值。
- 「地图区域」页签：在游戏纸地图上拖拽画区域（自动吸附 40m 网格，悬停显示
  与游戏 HUD 同口径的显示坐标），点已有区域编辑；选鱼默认列出本图名册
  （带基础权重），外来鱼种切「全库搜索」。挪威海显示海图与声呐区号网格，
  按区号建区暂未开放（数据层保留 `sea_zone` 字段，硬拒绝保存，见
  `internal/admin/fish_zones.go`）；无标定地图保留「手动输入」弹窗兜底。

后续的钓鱼抽取和咬钩计时会读取这些修改，无需重启服务器。

纸地图贴图来自客户端 `Data/maps` bundle（18 张 2048×2048）。官方客户端贴图
不入仓库（素材红线，同水层掩码口径）：用离线工具导出到部署包 `ref/paper_maps/`
（已在 .gitignore 的 `/ref/` 段内），后台按 `/maps/{贴图名}.webp` 运行时加载，
缺图时显示合成占位网格，工作台几何功能不受影响：

```text
python tools/extract_paper_maps.py --client-data 'D:/Games/rf4game/Game/rf4_x64_Data' [--force]
```

期望贴图名单从 `internal/fishmaps/fishmaps.go` 解析（标定与贴图名的单一事实源），
客户端资源更新后重跑该命令即可；贴图目录缺失时无需重建，刷新页面即生效。

“新吃饵逻辑”是全水域开关，默认关闭。开启后按地图名册、后台启禁、已识别饵料和本地实钓搭配筛鱼；没有匹配鱼时不使用备用鱼池。明确不吃的饵优先排除，关闭后恢复旧判鱼流程。开关独立于调试预设保存，最多 30 秒影响后续判鱼，不重新抽取已咬钩的鱼；GM 指定下一条鱼仍保留原行为。

海图区号已接入：挪威海按抛竿落点所在水域定位区号，攻略里写“海图 NN 号区域”的鱼只在对应区号咬钩，“海图，区域不限”只要求当前在海图上；定位不到落点时保持中性。顺水漂流、`jig阶梯`、饵在水中运动按 `14/7` 钓组动画状态位判定，没有动作上报时不据此限制上鱼。船票与上船按 3/7 验票有效期加 `3/10` boat_use 实时上报判定：验票通过或收到上船上报视为在船上，收到下船上报立即判离船；没收到下船上报时租用到期才算已下船，本次会话没见过上船流程时保持中性。双饵沿用已有实现：多钩逐钩用各自的饵判鱼并保留组合加成。仍未接入的是内陆湖攻略网格坐标，控制台会显示这些范围。未识别且没有实钓证据的搭配暂不放行。规则随服务端发布，本次不自动联网更新统计数据。

## 邮件

向玩家发送一条文本邮件，并可附带零个或多个目录物品。每项附件单独设置数量。邮件投递由 gameserver 执行。

## 审计日志

管理操作产生的变更会写入 `audit_log`。控制台提供只读的审计列表。

## API

统一入口下，以下路径均增加 `/admin` 前缀，例如登录为 `POST /admin/api/login`。除登录外，其他请求均需将 JWT 作为 bearer token 提交。独立调试 `cmd/admin` 时仍直接使用 `/api/*`。

```text
POST /api/login
GET  /api/me
GET  /api/dashboard
GET  /api/tunables
POST /api/tunables
GET  /api/players
POST /api/players/create
GET  /api/players/detail
POST /api/players/update
POST /api/players/grant
POST /api/players/items/delete
POST /api/players/keepnet/add
POST /api/players/keepnet/delete
POST /api/players/skills/update
POST /api/players/kick
POST /api/players/ban
GET  /api/catalog/items
GET  /api/catalog/groups
GET  /api/stores
GET  /api/stores/items
POST /api/stores/items/update
POST /api/stores/items/list
POST /api/stores/items/reset
GET  /api/stores/product?item_id=<商品ID>
POST /api/stores/product/update
GET  /api/custom-products/base?item_id=<商品ID>
GET  /api/custom-products?item_id=<定制商品ID>
POST /api/custom-products
POST /api/custom-products/update
GET  /api/fish/areas        （每行附 level / map_kind grid|sea|none / texture / calibration）
GET  /api/fish/config
POST /api/fish/config
POST /api/fish/config/reset
GET  /api/fish/pool
GET  /api/fish/zones
POST /api/fish/zones
POST /api/fish/zones/delete
GET  /api/fish/bite_mode
POST /api/fish/bite_mode
GET  /api/bait/prefs
GET  /api/bait/items
GET  /api/bait/categories
PUT  /api/bait/rules
POST /api/bait/rules/reset
POST /api/mail/send
GET  /api/audit
```

阶段奖励选择器先调用 `/api/catalog/groups?complete_only=1` 读取商品分类，再调用 `/api/catalog/items?complete_only=1&group=<key>` 读取当前分类。商品来自完整静态目录，因此活动专属商品即使没有 `shop_item` 货架记录也可以选择；竿坯、手柄、线轴和渔轮内部零件会被排除。普通目录调用不受这些筛选参数影响。

新年与周年活动均关闭时，阶段奖励编辑器处于禁用状态。开启任一活动后，奖励编辑器只显示已保存的活动分支；分支列表为空时不能编辑。移除分支会保留其已有奖励，重新加入后可以继续维护；“清空可选分支奖励”只清除当前可选分支的奖励。

活动分支 14、18 有 10 个阶段，其余活动分支有 7 个阶段；保存奖励时校验阶段是否存在和物品 ID 是否对应静态目录中的完整商品。数量表示实际件数：可堆叠物品合并，非堆叠装备逐件入包。游戏预览只显示物品种类，不表达数量；同物品多行配置的数量合计发放。关闭全部活动或从活动分支列表移除某分支后，该分支停止发奖，重新开启时可补发尚未领取的已完成阶段。

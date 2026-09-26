# Gotify → Bark 通知同步插件（Bark Forwarder）

把 **Gotify 收到的每一条通知**实时镜像推送到 **Bark**（iOS 推送），支持**自建 bark-server**。

纯插件实现：**不需要修改 Gotify 源码**，把编译好的 `.so` 丢进插件目录就能用。

```
┌─────────┐   WebSocket /stream    ┌──────────────┐   POST /push   ┌─────────────┐   APNs   ┌────────┐
│ Gotify  │ ─────────────────────► │ Bark Forwarder│ ─────────────► │ bark-server │ ───────► │ iPhone │
│  :8080  │   (client token 鉴权)   │   (本插件)    │                │   :8080     │          │  Bark  │
└─────────┘                        └──────────────┘                └─────────────┘          └────────┘
```

---

## 一、它是怎么工作的

Gotify 本身没有「收到消息」的插件钩子，所以本插件换了个思路：

1. 用你创建的 **Gotify 客户端令牌（client token）** 连上 Gotify 的 WebSocket 接口 `/stream`；
2. 该接口会把**这个用户能看到的全部通知**（所有应用、包括其它程序推来的）实时推过来；
3. 插件逐条把消息映射成 Bark 的推送参数，调用 `POST {server_url}/push`（开启加密后改为 `POST {server_url}/{device_key}` 并发送密文）；
4. 多个设备就是多次调用；失败自动重试 3 次（退避 0 / 0.5s / 1s），失败只写日志，**绝不会影响 Gotify 本身**。

因为走的是标准 API，所以：

- 不需要改 Gotify 一行代码；
- 也可以用来把 Gotify 的消息同步给**多个** Bark 设备（`device_keys` 填多个，逗号分隔）。

---

## 二、快速开始

### 1. 拿到插件文件

**方式 A：下载预编译产物（最省事）**

```bash
# x86_64 服务器 / NAS / 云主机
curl -LO https://gitea.example.com/dsh/-/packages/generic/gotify-bark-plugin/v1.1.0/bark-linux-amd64.so

# ARM64（树莓派 4/5、甲骨文 ARM、Apple Silicon 上的 Linux 虚拟机）
curl -LO https://gitea.example.com/dsh/-/packages/generic/gotify-bark-plugin/v1.1.0/bark-linux-arm64.so
```

也可以在包管理页面浏览下载：<https://gitea.example.com/dsh/-/packages>

| 文件 | 适用平台 |
| --- | --- |
| `bark-linux-amd64.so` | x86_64 服务器 / NAS / 云主机（绝大多数情况） |
| `bark-linux-arm64.so` | ARM64（树莓派 4/5、甲骨文 ARM、Apple Silicon 上的 Linux 虚拟机） |

这两个文件是针对 **官方 gotify v3.1.1 发布版**编译并**逐包校验过兼容性**的，可以直接用。

**方式 B：自己编译**

```bash
git clone https://gitea.example.com/dsh/gotify-bark-plugin.git
cd gotify-bark-plugin
./scripts/build.sh          # 产物输出到 build/ 目录
```

> 插件必须与 Gotify 本体「同源构建」才能加载。上面两种方式产出的 `.so` 都是针对**官方发布版**对齐的；
> 如果你用的是自己编译的 gotify，请见第 6 节「兼容性」。

### 2. 放进 Gotify 的插件目录

**Docker 部署：**

```bash
docker cp bark-linux-amd64.so gotify:/app/data/plugins/bark.so
docker restart gotify
```

**原生部署：**

```bash
cp bark-linux-amd64.so "$GOTIFY_PLUGINSDIR/bark.so"
# GOTIFY_PLUGINSDIR 未设置时默认为 gotify 工作目录下的 data/plugins
systemctl restart gotify
```

### 3. 在网页上配置

打开 Gotify → `Plugins` → **Bark Forwarder**，页面里就有完整图文步骤，照填即可。

最关键的三个字段：

| 字段 | 填什么 | 去哪找 |
| --- | --- | --- |
| `client_token` | Gotify **客户端**令牌 | Gotify 网页 → `Clients` → `Create Client`（令牌只显示一次） |
| `device_keys` | Bark 设备密钥 | 打开 Bark App 首页，复制推送 URL 中间那段 |
| `server_url` | Bark 服务端地址 | 自建填 `http://你的IP:8080`；用官方服务填 `https://api.day.app` |

保存后把 `enabled` 设为 `true`，页面状态变成「✅ 运行中」就成功了。

---

## 三、配置项完整说明

配置以 YAML 形式保存（网页上改就行，下面是等价形式）：

```yaml
enabled: true

# ── 必填三项 ─────────────────────────────────────────────
# Gotify 地址。留空则自动使用你访问插件页时的地址；
# Docker 部署下自动探测到的地址常常在容器内不可达，建议显式填写，例如 http://gotify:80
gotify_url: "http://192.168.1.10:8080"
# Gotify 客户端令牌（WebUI -> Clients -> Create Client）
client_token: "gtfyc.xxxxxxxx"
# Bark 设备密钥，多个用逗号/空格/换行分隔；也接受完整推送 URL（自动提取 key）
device_keys: "key1,key2"
# Bark 服务端；自建示例 http://192.168.1.10:8080
server_url: "https://api.day.app"

# ── 端到端加密（Bark App 里打开「加密」时才需要填）────────
# 32 位密钥，必须与 Bark App 中设置的完全一致；留空则明文推送
encrypt_key: ""
# 加密模式：cbc（默认，每条消息随机 IV）或 ecb
encrypt_mode: "cbc"
# CBC 模式的固定 IV（16 位）；一般留空，由插件每次随机生成
encrypt_iv: ""

# ── 推送外观 ─────────────────────────────────────────────
# 用 Gotify 的应用名作为 Bark 分组（推荐开启）
group_by_app: true
# 应用名为空时使用的兜底分组名
default_group: ""
# 中断级别：active（默认）/ timeSensitive / passive / critical，留空用 Bark 默认
level: "active"
# 铃声名，如 alarm、minuet、multiwayinvitation，留空用系统默认
sound: ""
# 通知图标 URL（iOS 15+）
icon: ""
# "1" = 响铃持续 30 秒
call: ""
# "1" = 自动把正文复制到剪贴板
auto_copy: ""
# 复制到剪贴板的内容模板，留空则复制正文
# 占位符：{appid} {appname} {title} {message} {priority} {messageid}
copy_template: ""
# 点击通知跳转的 URL，占位符同上（会自动做 URL 转义）
url_template: ""
# "none" = 点击通知不跳转
action: ""
# "1" = 在 Bark 历史记录中保留
archive: ""
# 历史记录保留秒数，0 = Bark 默认；大于 0 时会自动打开 archive
ttl: 0
# 角标数字，-1 = 不改动
badge: -1

# ── 过滤与行为 ───────────────────────────────────────────
# 低于该优先级的消息不推送（-2 ~ 10）
min_priority: -2
# 标题以该前缀开头的消息直接丢弃（可用来防止回环）
skip_title_prefix: ""
# 把 Gotify 的 extras（JSON）追加到正文
include_extras: false
# 单次 Bark 请求超时（秒）
timeout_seconds: 10
# 打印每条转发/跳过的日志
debug: false
# 只记日志、不真正推送到 Bark（调试试用）
dry_run: false
```

### 常用技巧

- **只推重要消息**：`min_priority: 5`，Gotify 里 priority ≥ 5 的才会推。
- **防止自己推给自己造成回环**：给回环消息的标题加个前缀（比如 `[回环]`），再设 `skip_title_prefix: "[回环]"`。
- **一眼看出是哪台机器**：`group_by_app` 开着，Bark 里就会按 Gotify 应用名分组。
- **点击通知直达 Gotify 消息页**：内置占位符只有 `{appid}` `{appname}` `{title}` `{message}` `{priority}` `{messageid}`，所以地址要写全，例如
  `url_template: "http://192.168.1.10:8080/#/messages"`。

---

## 三点五、端到端加密推送

Bark 支持**端到端加密**：通知正文在推送前就被加密，**bark-server 只能看到密文**，只有你的 iPhone 能解密。自建服务器给别人用、或者服务端跑在公网上时，强烈建议开启。

参考：[Bark 官方加密文档](https://bark.day.app/#/encryption)

### 配置方法

**第一步，在 Bark App 里开启加密并设置密钥。**

打开 Bark App → 右上角进设置 → 打开「加密」→ 填入一个 **32 位** 的密钥（字母数字符号都行，务必记牢，丢了就解不开）。

**第二步，把同一个密钥填进插件。**

```yaml
encrypt_key: "你设置的那个32位密钥"
encrypt_mode: "cbc"     # 默认值，一般不用改
encrypt_iv: ""          # 留空即可
```

保存后插件会自动切换到加密通道，日志里会显示 `(encrypted, mode=cbc)`。

### 两种模式怎么选

| 模式 | 说明 | 建议 |
| --- | --- | --- |
| `cbc` | 每条消息随机生成 IV，相同内容也会产生不同密文 | **默认选这个**，更安全 |
| `ecb` | 不传 IV，相同内容产生相同密文 | 仅在 App 端只支持 ECB 时使用 |

`encrypt_iv` 只有在必须复现固定密文时才需要填（16 位）；填了之后所有消息共用同一个 IV，**安全性会下降**，平时请留空。

### 插件到底做了什么

开启加密后，插件的行为会变成（与 Bark 官方脚本完全等价）：

| | 明文模式（默认） | 加密模式 |
| --- | --- | --- |
| 请求地址 | `POST {server_url}/push` | `POST {server_url}/{device_key}` |
| 请求格式 | JSON | 表单 `ciphertext=...&iv=...` |
| 正文 | 明文 JSON | AES-256-CBC 密文（Base64） |
| 设备密钥位置 | JSON 字段 `device_key` | URL 路径 |

加密细节：`AES-256` + `PKCS#7` 填充，密钥为 32 个 ASCII 字符，IV 为 16 个 ASCII 字符 —— 与 Bark 文档里那段 `openssl enc -aes-256-cbc` 脚本产出**逐字节一致**（仓库里的 `bark_test.go` 就是用官方示例的密文做断言的）。

> ⚠️ 注意：Bark 文档的脚本里 `xxd -ps` 那两行只是为了满足 `openssl -K/-iv` 要求十六进制输入，**真正发出去的 `iv` 参数是那 16 个原始字符**，不是它的十六进制形式。插件直接按原始字符处理，两边结果相同。
>
> ⚠️ 加密模式下 `device_key` 不再是 JSON 字段：插件会把它拼进 URL，这正是 Bark 加密接口的设计。

---

## 四、自建 bark-server

### 用 Docker（推荐）

```bash
docker run -d --name bark-server \
  -p 8080:8080 \
  -v $(pwd)/bark-data:/data \
  -e BARK_SERVER_DATA_DIR=/data \
  finab/bark-server
```

### 用二进制

到 [bark-server releases](https://github.com/Finb/bark-server/releases) 下载对应架构的文件：

```bash
chmod +x bark-server_linux_amd64
BARK_SERVER_ADDRESS=0.0.0.0:8080 BARK_SERVER_DATA_DIR=./data ./bark-server_linux_amd64
```

### 接到插件上

1. 在 Bark App 里把服务器地址改成 `http://你的IP:8080`（App 首页 → 右上角 → 服务器）；
2. 回到 App 首页，复制推送 URL 中间那段 key；
3. 插件配置里 `server_url: http://你的IP:8080`、`device_keys: 刚才的key`。

> **验证服务是否正常**：`curl http://你的IP:8080/ping` 应返回 `{"code":200,"message":"pong"}`。

---

## 五、创建 Gotify 客户端令牌

1. 打开 Gotify 网页，左下角 → `Clients` → `Create Client`；
2. 名称随便填（例如 `bark`），权限用默认的即可；
3. 创建后弹出的那串令牌（形如 `gtfyc.xxxxxx`）**只显示一次**，立刻复制；
4. 粘到插件配置的 `client_token`。

> 注意：不要填成应用的令牌（`gtfya.` 开头，那是给发消息用的），也不要填密码。

---

## 六、兼容性（重要，请务必读一下）

### 为什么插件必须「同源构建」

Go 的插件机制在加载 `.so` 时会**逐个包比对编译产物的指纹**（编译器导出数据的哈希）。
只要有一个包对不上，Gotify 就会拒绝加载，并报出类似这样的错误：

```
plugin was built with a different version of package github.com/gorilla/websocket
```

指纹受这些因素影响：

1. **Go 版本**必须完全一致（官方 gotify v3.1.1 用的是 `go1.26.0`）；
2. **所有依赖库的版本**必须与 Gotify 本体一致（本项目 `go.mod` 已按 gotify v3.1.1 锁定）；
3. `-buildmode=plugin` 会强制所有包带 `-dynlink`，此时编译器会把**源码目录的绝对路径**也写进导出数据 —— 所以 **GOROOT / 模块缓存的路径也必须一致**。

官方发布版的构建环境是固定：

```
GOROOT=/usr/local/go        GOMODCACHE=/go/pkg/mod
镜像：gotify/build:1.26.0-linux-amd64
```

`scripts/build.sh` 处理了第 3 点：把本机的模块缓存路径**重写**成官方的 `/go/pkg/mod`，
并单独排除 `runtime/cgo`（它是 GOROOT 内的包，编译器本来就隐藏 GOROOT 路径，额外重写反而会破坏指纹）。

### 本项目的验证结果

| 对照的官方二进制 | 共同包数 | 指纹一致 |
| --- | --- | --- |
| `gotify-linux-amd64` (v3.1.1) | 313 | **313 / 313 ✅** |
| `gotify-linux-arm64` (v3.1.1) | 312 | **312 / 312 ✅** |

也就是说，本项目编译出的两个 `.so` 与**官方发布版**是完全匹配的。

### 如果你的 Gotify 不是官方发布版

比如从源码自己编译、或用了发行版仓库里的包，那么上面的路径对齐就不适用了。
这种情况下请这样编译插件：

```bash
# 用与编译 gotify 本体完全相同的 Go 版本、同一个 GOROOT、同一个 GOMODCACHE
MATCH_LOCAL=1 ./scripts/build.sh amd64
```

`MATCH_LOCAL=1` 会关闭路径重写，让插件直接使用本机的真实路径 —— 前提是编译 gotify 本体时用的是同一套环境。

最稳妥的做法：直接用官方的 `gotify/build:<版本>-<架构>` 镜像编译插件（见 `Dockerfile`）。

---

## 七、自己构建

### 本机构建

需要 **Go 1.26.0** 和 `gcc`：

```bash
./scripts/build.sh            # amd64（若装了交叉编译器则连 arm64 一起）
./scripts/build.sh amd64      # 只编 amd64
./scripts/build.sh arm64      # 只编 arm64
```

交叉编译 arm64 需要 `gcc-aarch64-linux-gnu`：

```bash
sudo apt install gcc-aarch64-linux-gnu
```

### 用 Docker 构建（最省事，也最不容易出错）

```bash
docker build --build-arg GOARCH=amd64 -o build .
docker build --build-arg GOARCH=arm64 -o build .
```

`Dockerfile` 使用官方的 `gotify/build` 镜像，容器内路径天然就是 `/usr/local/go` 和 `/go/pkg/mod`，
所以**不需要任何路径重写**，也不要用 `-trimpath`。

> ⚠️ 千万不要加 `-trimpath`：它会让 `runtime/cgo` 的导出数据变化，导致加载失败。

---

## 八、验证与排错

### 自检接口

插件注册了一个只读的健康检查端点，可直接探活：

```bash
curl http://你的gotify/plugin/1/custom/<plugin-token>/bark
```

返回示例：

```json
{"devices":2,"enabled":true,"gotify_url":"http://127.0.0.1:18080",
 "plugin":"gotify-bark-plugin","running":true,"server_url":"http://127.0.0.1:18081",
 "version":"1.0.0"}
```

`running: true` 表示 WebSocket 已连上、转发线程在跑。

### 常见问题

| 现象 | 原因 / 解决 |
| --- | --- |
| 日志出现 `was built with a different version of package ...` | 插件与 Gotify 本体不是同源构建，见第 6 节 |
| 页面状态「无法确定 Gotify 地址」 | Docker 里自动探测的地址容器内不可达，手动填 `gotify_url`（如 `http://gotify:80`） |
| 状态「缺少 client_token」 | 填的是应用令牌或密码，请用 `Clients` 里的客户端令牌 |
| 状态「缺少 device_keys」 | 没填设备密钥，或填成了完整 URL 却没被识别（直接填 App 首页那段 key 最稳） |
| 日志 `gotify rejected the token (HTTP 401/403)` | 客户端令牌无效或已被删除，重建一个 |
| 日志 `gotify stream endpoint not found (HTTP 404)` | `gotify_url` 写错，或反向代理没把 `/stream` 转发过去 |
| 日志 `push to device ... connection refused` | bark-server 没启动 / 地址或端口不对 |
| bark-server 返回 `failed to get device token` | Bark App 还没在这个服务端注册过：在 App 里切换服务器地址后重开一次 |
| 改了配置没生效 | 保存后确认 `enabled: true`；配置变更会自动重启订阅，无需重启 Gotify |
| `plugin is disabled` | 插件被停用了，在 Plugins 页面点 Enable |

---

## 九、已验证环境

功能与兼容性都在真实环境中跑过：

- **Gotify**：官方发布版 `v3.1.1`（amd64、arm64），未做任何源码改动；
- **Bark 服务端**：真实 `bark-server v2.3.6` + 自建 mock 服务；
- 通过项：
  - 插件加载，无指纹报错；
  - WebSocket 订阅、消息实时转发、多设备分发；
  - `min_priority` 优先级过滤；
  - 分组（`group_by_app`）、级别、铃声、图标、`url_template` 等参数正确送达服务端；
  - 配置热更新、Enable / Disable 即时生效（毫秒级返回）；
  - bark-server 宕机时自动重试并只记日志，Gotify 不受影响；
  - 断线自动重连（指数退避 2s → 60s）；
  - **端到端加密**：CBC（随机 IV）、CBC（固定 IV）、ECB 三种模式的实际请求均已抓包解密核对，
    密文内容与发送的中文/表情正文完全一致；加密实现与 Bark 官方示例的密文**逐字节相同**（见 `bark_test.go`）。

---

## 十、文件说明

| 文件 | 作用 |
| --- | --- |
| `main.go` | 插件入口，实现 `GetGotifyPluginInfo` / `NewGotifyPluginInstance` |
| `plugin.go` | 插件主体：生命周期、转发队列、过滤、消息映射 |
| `gotify.go` | Gotify 侧：`/stream` WebSocket 订阅、应用名缓存、重连 |
| `bark.go` | Bark 侧：`POST /push` 请求与重试 |
| `config.go` | 配置结构、默认值、校验与规范化 |
| `display.go` | 插件页面上的中文图文说明 |
| `bark_test.go` | 加密与请求路由的单元测试（含 Bark 官方示例密文的断言） |
| `scripts/build.sh` | 构建脚本（含包指纹对齐逻辑） |
| `Dockerfile` | 用官方镜像构建 |
| `build/*.so` | 编译产物（不纳入版本管理，由 `scripts/build.sh` 生成，或从 Releases 下载） |

---

## 十一、许可

MIT

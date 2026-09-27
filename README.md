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

**方式 A：从 Releases 下载（推荐，无需登录）**

打开 <https://github.com/LOVE2CMOL/gotify-bark-plugin/releases>，选最新版本，在页面底部的 **Assets** 里下载对应架构的 `.so`：

```bash
# x86_64 服务器 / NAS / 云主机
curl -LO https://github.com/LOVE2CMOL/gotify-bark-plugin/releases/download/v1.2.2/bark-linux-amd64.so

# ARM64（树莓派 4/5、甲骨文 ARM、Apple Silicon 上的 Linux 虚拟机）
curl -LO https://github.com/LOVE2CMOL/gotify-bark-plugin/releases/download/v1.2.2/bark-linux-arm64.so
```

| 文件 | 适用平台 |
| --- | --- |
| `bark-linux-amd64.so` | x86_64 服务器 / NAS / 云主机（绝大多数情况） |
| `bark-linux-arm64.so` | ARM64（树莓派 4/5、甲骨文 ARM、Apple Silicon 上的 Linux 虚拟机） |

这两个文件是针对 **官方 gotify v3.1.1 发布版**编译并**逐包校验过兼容性**的，可以直接用。

**方式 B：自己编译**

```bash
git clone https://github.com/LOVE2CMOL/gotify-bark-plugin.git
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

### ⚠️ 从 v1.2.1 及更早版本升级（v1.2.2 起要重新配置一次）

v1.2.2 把插件的 `ModulePath` 改成了 `https://github.com/love2cmol/gotify-bark-plugin`。
Gotify 正是用这个字段识别插件的，所以换上新的 `.so` 并重启之后：

1. Plugins 页面里会**多出一个全新的插件**（新的 id、新的 token），旧的那一条不再显示；
2. 旧插件里的配置**不会继承**，需要在新插件页面重新填写一遍；
3. 新插件默认是**未启用**状态 —— 填完配置后还要点一下 **Enable**，状态变成「✅ 运行中」才会开始转发。

配置内容照抄原来的即可（`device_keys` / `server_url` / `client_token` 三项必填）。
旧记录会留在数据库里，不再显示，也不影响使用。

---

## 三、配置项完整说明

配置以 YAML 形式保存（网页上改就行，下面是等价形式）：

```yaml
enabled: true

# ── 必填三项 ─────────────────────────────────────────────
# Gotify 地址。留空则自动使用你访问插件页时的地址；
# Docker 下自动探测的地址常常在容器内不可达，建议显式填写。
# 注意它需要「容器内」和「手机」都能访问（见第四节末尾的说明），
# 最省事的填法就是直接写你平时访问 Gotify 的那个域名
gotify_url: "https://gotify.example.com"
# Gotify 客户端令牌（WebUI -> Clients -> Create Client）
client_token: "gtfyc.xxxxxxxx"
# Bark 设备密钥。多台设备就填多个，用逗号/空格/换行分隔，也接受完整推送 URL（自动提取 key）；
# ⚠️ 每台设备必须使用各自的 key：同一个 key 装在两台手机上，只有后打开 App 的那台能收到（见三点六）
device_keys: "key1,key2"
# Bark 服务端；自建示例 http://192.168.1.10:8080
# 不写 http:// 或 https:// 也行，插件会自动判断：bark、localhost、内网 IP 用 http，
# 公网域名用 https（写全了最保险）
server_url: "https://api.day.app"

# ── 端到端加密（Bark App 里打开「加密」时才需要填）────────
# 密钥长度决定算法：16 位 = AES128，24 位 = AES192，32 位 = AES256；
# 必须与 Bark App 里填的完全一致，留空则明文推送
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
# 中断级别：auto = 跟随 Gotify 优先级自动判断（推荐，见下方说明）
# 也可固定为 active（默认）/ timeSensitive / passive / critical，留空用 Bark 默认
level: "auto"
# 铃声名，如 alarm、minuet、multiwayinvitation，留空用系统默认
sound: ""
# 通知图标 URL（iOS 15+）；填 auto = 直接使用 Gotify 里给该应用设置的图标
icon: ""
# "1" = 允许响铃 30 秒；只有 Gotify 优先级 ≥ 9 时才会真正触发
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

### 优先级怎么变成中断级别（`level: auto`）

Bark 只有 4 档中断级别，而 Gotify 用的是 0-10 的优先级。填 `level: auto` 就会自动换算，不用一刀切：

| Gotify 优先级 | Bark 中断级别 | 手机上的表现 |
| --- | --- | --- |
| < 1（含 0 和负数） | `passive` | 静默，只进通知中心，不亮屏不响 |
| 1 ~ 3 | `active` | 普通通知，正常提示音 |
| 4 ~ 7 | `timeSensitive` | 可穿透「专注模式」 |
| > 7（8、9、10） | `critical` | 可穿透静音开关，最强提醒 |

于是日常消息安安静静地来，真正紧急的（priority 8 以上）才会强势提醒。

> `critical` 需要在 Bark App 里单独开启权限，否则 iOS 会忽略该级别。
> 如果不想自动换算，把 `level` 写成 `active` / `timeSensitive` / `passive` / `critical` 里的任意一个即可固定。

### 两个 `auto` / 高优先级的贴心设计

- **`icon: auto`** —— Bark 通知左侧的图标直接使用你在 Gotify 里给该应用设置的那个图标（应用没单独设置则用 Gotify 默认图标）。
  注意：图标是 iPhone 自己去拉的，所以如果 Gotify 是内网地址，手机得能访问到它。
  插件会缓存应用信息 5 分钟，所以刚在 Gotify 里改完图标，最多 5 分钟后才会反映到推送里。
- **`call` 只在优先级 ≥ 9 时触发** —— `call: "1"` 并不会让所有通知都响 30 秒，
  只有 Gotify 优先级 **≥ 9** 的消息才会真正触发持续响铃，其余照常安静推送。
  所以可以放心全局打开 `call`，不用担心一条普通消息把手机响炸。

---

## 三点五、端到端加密推送

Bark 支持**端到端加密**：通知正文在推送前就被加密，**bark-server 只能看到密文**，只有你的 iPhone 能解密。自建服务器给别人用、或者服务端跑在公网上时，强烈建议开启。

参考：[Bark 官方加密文档](https://bark.day.app/#/encryption)

### 配置方法

**第一步，在 Bark App 里设置加密参数。**

打开 Bark App 首页 → **推送加密** → 加密设置，几个选项必须和插件对上：

| App 里的选项 | 选什么 | 对应插件 |
| --- | --- | --- |
| 算法 | **AES128 / AES192 / AES256 都行** | 由 `encrypt_key` 的**长度**决定，见下 |
| 模式 | **CBC** | `encrypt_mode: cbc`（默认值） |
| Padding | **pkcs7** | 插件使用 PKCS#7 填充 |
| Key | 长度与所选算法一致 | `encrypt_key` 填一模一样的内容 |

**算法不需要手动对应** —— Bark 是按**密钥长度**挑算法的，插件也一样：

| `encrypt_key` 长度 | 算法 | App 里选 |
| --- | --- | --- |
| 16 位 | AES-128 | AES128 |
| 24 位 | AES-192 | AES192 |
| 32 位 | AES-256 | AES256 |

所以照着 Bark 官方文档里那段 **16 位密钥**的示例脚本填也完全没问题，插件会自动走 AES-128。

多台设备时**每台都要设置一遍，并且填同一个密钥**（设备 key 则是各用各的，见三点六）。

> ⚠️ **模式里的 `GCM` 暂不支持**，请选 `CBC`（推荐）或 `ECB`。

**第二步，把同一个密钥填进插件。**

```yaml
encrypt_key: "与 App 里完全一致的密钥"   # 16 / 24 / 32 位，长度决定算法
encrypt_mode: "cbc"                     # 默认值，一般不用改
encrypt_iv: ""                          # 留空即可
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
| 正文 | 明文 JSON | AES 密文（Base64） |
| 设备密钥位置 | JSON 字段 `device_key` | URL 路径 |

加密细节：`AES` + `PKCS#7` 填充，**密钥长度决定算法**（16 / 24 / 32 个 ASCII 字符 → AES-128 / AES-192 / AES-256），IV 为 16 个 ASCII 字符 —— 与 Bark 文档里那段 `openssl enc -aes-<位数>-cbc` 脚本产出**逐字节一致**（仓库里的 `bark_test.go` 用官方示例的密文做断言，AES-128 和 AES-256 两套官方向量都在里面）。

> ⚠️ 注意：Bark 文档的脚本里 `xxd -ps` 那两行只是为了满足 `openssl -K/-iv` 要求十六进制输入，**真正发出去的 `iv` 参数是那 16 个原始字符**，不是它的十六进制形式。插件直接按原始字符处理，两边结果相同。
>
> ⚠️ 加密模式下 `device_key` 不再是 JSON 字段：插件会把它拼进 URL，这正是 Bark 加密接口的设计。

---

## 三点六、多台 iOS 设备

想让多台 iPhone / iPad 都收到同一条通知，只需要把它们的设备 key 都填进 `device_keys`。

### 填法

```yaml
device_keys: "key1,key2,key3"
```

分隔符很宽容 —— 逗号、分号、空格、制表符、换行都认，下面几种写法完全等价：

```yaml
device_keys: "key1,key2"
device_keys: "key1 key2"
device_keys: "key1;key2"
```

也支持直接粘**完整推送 URL**，插件会自动抠出中间那段 key：

```yaml
device_keys: "https://bark.example.com/jvzyVMdNYH3SxmU9GhAyM3, https://bark.example.com/另一台的key"
```

重复的 key 会**自动去重**，不会重复推两遍。每台设备的 key 就在各自 Bark App 的首页（那条推送 URL 的中间一段）。

### ⚠️ 每台设备必须使用不同的 key

Bark 官方 FAQ 写得很明确：

> 多台设备使用同一个 key，但只有其中一台设备可以收到推送。**同一个 Key 只能一台设备使用，只有最后打开的 App 会收到推送。**

所以**不要**把同一台手机的推送 URL 原样复制到第二台手机。正确做法是在每台设备上各自打开 Bark App 让它注册，然后收集各自的 key。

### 多设备时的行为

| 项目 | 表现 |
| --- | --- |
| 推送方式 | 每台设备**各发一次**请求，串行发送 |
| 某台失败 | **只记日志，继续推下一台** —— 一台离线不影响其他设备 |
| 推送内容 | 所有设备**完全一致**：同样的级别、铃声、图标、分组、`call` |
| 数量 | 三五台无感；几十台才会明显变慢（耗时随设备数线性增长） |

### 加密时的两条铁律

| 配置项 | 多台设备之间 | 说明 |
| --- | --- | --- |
| `device_keys` | **必须各不相同** | 相同 key 只有最后打开 App 的那台能收到 |
| `encrypt_key` | **必须完全相同** | 加密是端到端的，每台 App 用本地密钥各自解密，而插件只有一份密钥 |

一句话：**同一个密钥发给所有设备，但每台设备的设备 key 各不相同。**

> 哪台设备忘了在 App 里配加密（或者密钥、算法不一致），它的通知正文会直接显示 `Decryption Failed`。
> 这是 Bark App 里写死的提示，看到它就说明那台设备的加密配置有问题。

### 高优先级提醒

`call: "1"` 在 Gotify 优先级 ≥ 9 时触发 30 秒持续响铃，而这是**全设备**一起响。
半夜一条 critical，全家手机会同时尖叫 —— 心里有个数。

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

### Gotify 与 Bark 在同一台机器上：走容器内部网络

如果两个容器由同一个 `docker-compose.yml` 管理，可以让插件**按容器名直连** Bark，不走公网域名 —— 少绕一圈反向代理，反代挂了推送也不会断。

默认的 `network_mode: bridge` 会让容器只能用「宿主机 IP + 映射端口」互访，**容器名无法解析**。把这两行删掉，compose 就会把同一个文件里的服务放进同一张自定义网络，服务名可以直接当域名用：

```yaml
services:
  gotify:
    container_name: gotify-20230112
    image: gotify/server:latest
    ports:
      - 10086:80
    # network_mode: bridge        ← 删掉

  bark:
    container_name: bark-server
    image: finab/bark-server:latest
    command: ["bark-server", "-dsn=bark:xxx@tcp(192.168.1.10:3306)/bark"]
    ports:
      - 10087:8080
    # network_mode: bridge        ← 删掉
```

（想显式声明的话，也可以给两个服务都加 `networks: [gotify-bark]`，文件末尾再写 `networks: { gotify-bark: { driver: bridge } }`，效果一样。）

然后插件配置里：

```yaml
server_url: "http://bark:8080"   # 服务名:容器内端口 —— 不是宿主映射出去的 10087
```

三个容易踩的坑：

1. **必须写全 `http://`**。插件在地址里看不到 `://` 时会自动补 `https://`，于是 `bark:8080` 会变成 `https://bark:8080` —— 内网没有证书，连接直接失败。
2. **端口写容器内的 `8080`**，不是宿主机映射的 `10087`。
3. **`gotify_url` 不要改成本地回环地址**。插件除了用它订阅消息流，还会用它拼出 `icon: auto` 的图标地址发给手机；写成 `http://127.0.0.1` 后手机拉不到图标。填手机也能访问的地址即可。

改完重建容器，再验证一次：

```bash
docker compose up -d
docker exec gotify-20230112 wget -qO- http://bark:8080/ping
# 期望输出：{"code":200,"message":"pong"}
```

> Bark App 里填的服务器地址（那个公网域名）**不用改**：那是手机拉取通知用的，和插件推送是两条独立的路，只要指向同一个 bark-server 实例就行。

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

### 发布新版本

编译产物挂在 **GitHub Releases**，一条命令搞定：

```bash
./scripts/build.sh all                                  # 先生成 build/bark-linux-*.so
GITHUB_TOKEN=xxx ./scripts/release.sh v1.2.3            # 打标签 + 建 Release + 上传附件
```

脚本会计算 sha256、创建 Release、把两个 `.so` 作为附件挂到发布页面，并在结束时打印发布页地址。

如果同时还维护了自建的 Gitea 镜像，再补上这三个变量即可**一次发两边**：

```bash
GITHUB_TOKEN=xxx \
GITEA_URL=https://gitea.example.com GITEA_TOKEN=yyy GITEA_OWNER=yourname \
./scripts/release.sh v1.2.3
```

> ⚠️ Gitea 的 Release 附件受 `[attachment] MAX_SIZE` 限制（默认 **4 MB**），而插件产物约 30 MB。
> 需要服务端先调大：`app.ini` 里写 `[attachment] MAX_SIZE = 64`，Docker 部署可加环境变量
> `GITEA__attachment__MAX_SIZE=64`，重启 Gitea 生效。未调大时该平台的上传会返回 413，脚本会直接提示。

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
| 通知正文显示 `Decryption Failed` | App 与插件的密钥/算法/模式对不上：核对 `encrypt_key` 是否一字不差、密钥长度与 App 选的算法是否对应（16/24/32 → AES128/192/256）、模式是否 CBC（见三点五） |
| 两台 iPhone 只有一台能收到 | 两台用了**同一个** `device_key`。Bark 规定一个 key 只能一台设备使用，每台需要各自注册 key（见三点六） |

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
    密文内容与发送的中文/表情正文完全一致；`AES-128 / AES-192 / AES-256` 三种密钥长度均可正常工作
    （16 位与 24 位已实机抓包解密验证）；加密实现与 Bark 官方示例的密文**逐字节相同**（见 `bark_test.go`，
    含 Bark 文档中英文两套 AES-128 官方向量）；
  - **包指纹**：与官方 gotify v3.1.1 二进制的公共包指纹比对，amd64 313/313、arm64 312/312 全部一致。

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
| `scripts/release.sh` | 发布脚本：打标签、建 Release、把编译产物挂到发布页面 |
| `Dockerfile` | 用官方镜像构建 |
| `build/*.so` | 编译产物（不纳入版本管理，由 `scripts/build.sh` 生成，或从 Releases 下载） |

---

## 十一、许可

MIT

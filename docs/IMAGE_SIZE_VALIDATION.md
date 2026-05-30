# gpt-image-2 图像尺寸处理与校验


> 注意：项目中模型 ID 是 `gpt-image-2`（以及别名 `gpt-image-2-2k`、`gpt-image-2-4k`），而非 `gpt-images-2`。

---

## 1. 模型与常量

定义于 `proxy/images.go:26-46`。

| 常量 | 值 | 含义 |
|------|-----|------|
| `defaultImagesToolModel` | `gpt-image-2` | 图像工具的规范模型 ID |
| `imageModel2KAlias` | `gpt-image-2-2k` | 2K 别名 |
| `imageModel4KAlias` | `gpt-image-2-4k` | 4K 别名 |
| `maxGPTImage2Pixels` | `8294400` | 总像素上限（= 3840 × 2160） |

### 默认尺寸常量

| 档位 | 默认 / 方形 | 横版 (landscape) | 竖版 (portrait) |
|------|-------------|------------------|-----------------|
| 1K | `1024x1024` | `1536x864` | `864x1536` |
| 2K | `2048x2048` | `2560x1440` | `1440x2560` |
| 4K | `3840x2160`（方形 `2880x2880`） | `3840x2160` | `2160x3840` |

---

## 2. 处理流程概览

```
请求进入
  │
  ├─ normalizeImageToolModelForPrompt(model, prompt)
  │     ├─ 将别名(2k/4k)归一化为 gpt-image-2
  │     └─ 根据 prompt 推断默认尺寸（档位 × 横/竖/方）
  │
  ├─ setDefaultImageToolSize(tool, defaultSize)
  │     └─ 仅当请求未显式给出 size 时，填入推断的默认尺寸
  │
  └─ validateResponsesImageGenerationSizes(body)   ← /v1/responses 入口校验
        └─ 对每个 image_generation 工具调用 validateGPTImage2Size(size)
```

---

## 3. 模型归一化与默认尺寸推断

### 3.1 `normalizeImageToolModelForPrompt(model, prompt) -> (toolModel, defaultSize)`

`proxy/images.go:351`

将客户端传入的模型映射为规范模型，并根据档位 + prompt 推断默认尺寸：

| 输入 model（忽略大小写） | 归一化为 | 默认尺寸来源档位 |
|--------------------------|----------|------------------|
| 空字符串 或 `gpt-image-2` | `gpt-image-2` | 1K 档 |
| `gpt-image-2-2k` | `gpt-image-2` | 2K 档 |
| `gpt-image-2-4k` | `gpt-image-2` | 4K 档 |
| 其他 | 原样返回 | 空（不推断） |

### 3.2 `inferDefaultImageSize` / `inferImageAspectFromPrompt`

`proxy/images.go:380` / `:398`

从 prompt 文本（忽略大小写）推断画幅方向，选择对应档位的尺寸。命中规则按以下优先级：

1. **显式方向关键字**
   - 方形：`方图`、`方形`、`正方形`、`square`、`1:1`
   - 竖版：`竖版`、`竖屏`、`纵向`、`竖向`、`手机壁纸`、`手机屏保`、`手机海报`、`portrait`、`vertical`、`phone wallpaper`、`mobile wallpaper`、`9:16`
   - 横版：`横版`、`横屏`、`横向`、`宽屏`、`桌面壁纸`、`电脑壁纸`、`电脑桌面`、`landscape`、`horizontal`、`wide`、`widescreen`、`desktop wallpaper`、`16:9`
2. **题材推断（弱）**
   - 方形：`头像`、`图标`、`徽标`、`贴纸`、`表情包`、`logo`、`icon`、`avatar`、`sticker`
   - 竖版：`海报`、`poster`、`封面`、`cover`
   - 横版：`壁纸`、`wallpaper`、`电影感`、`cinematic`、`banner`、`横幅`
3. 都未命中 → 使用该档位的 `defaultSize`（方形）

### 3.3 `setDefaultImageToolSize(tool, defaultSize)`

`images.go:435`

仅在 **请求未显式提供 `size`** 时，把推断出的默认尺寸写入工具。已有 `size` 则保持不变。

---

## 4. 尺寸校验规则

### 4.1 触发条件 — `shouldValidateGPTImage2Size`

`images.go:444`

仅当模型经归一化后等于 `gpt-image-2`（`defaultImagesToolModel`）时才执行尺寸校验。其他模型跳过。

### 4.2 核心校验 — `validateGPTImage2Size(size) error`

`images.go:449`

按顺序应用以下规则，任一不满足即返回错误：

| # | 规则 | 条件 | 错误信息 |
|---|------|------|----------|
| 0 | **放行** | `size` 为空 或 等于 `auto`（忽略大小写） | —（直接通过，不再校验） |
| 1 | **格式** | 以小写 `x` 分割后恰好得到两段 | `image size %q must use WIDTHxHEIGHT format or auto` |
| 2 | **宽度有效** | 宽可解析为整数且 `> 0` | `image size %q has invalid width` |
| 3 | **高度有效** | 高可解析为整数且 `> 0` | `image size %q has invalid height` |
| 4 | **16 的倍数** | `width % 16 == 0` 且 `height % 16 == 0` | `image size %q is invalid: width and height must be multiples of 16` |
| 5 | **总像素上限** | `width × height ≤ 8294400` | `image size %q is invalid: total pixels %d exceeds max %d` |
| 6 | **长宽比** | 长边 ≤ 短边 × 3（即 ≤ 3:1） | `image size %q is invalid: aspect ratio must not exceed 3:1` |

实现要点：
- 解析前会 `TrimSpace` 并转小写；分隔符是字符 `x`。
- 像素与比例比较使用 `int64`，避免溢出。
- 长边/短边通过比较 `width` 与 `height` 动态确定，因此横竖版同等受 3:1 限制。

### 4.3 入口校验 — `validateResponsesImageGenerationSizes(body) error`

`images.go:484`

针对 `/v1/responses` 请求体：

1. 读取 `tools` 数组；不存在或非数组 → 直接通过。
2. 遍历每个 tool：
   - 跳过 `type != "image_generation"` 的工具。
   - 跳过 `shouldValidateGPTImage2Size` 判定为 false 的（非 gpt-image-2）。
   - `size` 不存在或为 `null` → 跳过。
   - `size` 存在但**不是字符串** → 报错：`image_generation tool %d size must be a string like 1024x1024 or auto`。
   - 否则对 `size` 调用 `validateGPTImage2Size`，错误包装为：`image_generation tool %d: <原始错误>`。

---

## 5. HTTP 行为

校验在 `forwardImagesRequest` 入口处执行：

```go
if err := validateResponsesImageGenerationSizes(responsesBody); err != nil {
    c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
        "message": "Invalid request: " + err.Error(),
        "type":    "invalid_request_error",
    }})
    return
}
```

- 校验失败 → 返回 **HTTP 400**，`type` 为 `invalid_request_error`，`message` 形如 `Invalid request: image_generation tool 0: image size "1000x1000" is invalid: width and height must be multiples of 16`。
- 校验通过 → 继续向上游转发。

---

## 6. 示例

| size 输入 | 结果 | 原因 |
|-----------|------|------|
| `auto` / 空 | ✅ 通过 | 放行规则 |
| `1024x1024` | ✅ 通过 | 16 倍数，1.05M 像素，1:1 |
| `3840x2160` | ✅ 通过 | 恰好等于像素上限 8294400，16:9 |
| `2880x2880` | ✅ 通过 | 8.29M 像素，未超上限 |
| `1000x1000` | ❌ 400 | 不是 16 的倍数 |
| `1024` | ❌ 400 | 格式错误（无 `x`） |
| `0x1024` | ❌ 400 | 宽度无效 |
| `4096x2160` | ❌ 400 | 8847360 > 8294400，超像素上限 |
| `3072x1024` | ❌ 400 | 3:1 = 通过；`3088x1024` 则超比例 |

---


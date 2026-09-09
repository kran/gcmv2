package types

import "fmt"

// galleryKind 相册 — 值 = []string（图片路径数组）。
// 与 array<upload-image> 区别: 独立 kind（kind 名 = admin 控件名 "gallery" —
// 前端按 kind 渲染相册组件: 网格缩略 + 批量上传 + 拖拽排序 + 删除 — 非逐项长表单）。
// 存储: fields JSON 里的字符串数组（imgproc/oss 动态拼尺寸 — 不存元信息）。
type galleryKind struct{}

// KindGallery kind 名; WidgetGallery 相册编辑控件。
const KindGallery = "gallery"

func (galleryKind) Name() string { return KindGallery }

func (galleryKind) Validate(v any) error {
	arr, ok := v.([]any)
	if !ok {
		return fmt.Errorf("expects array of image paths, got %T", v)
	}
	for i, e := range arr {
		if _, ok := e.(string); !ok {
			return fmt.Errorf("gallery[%d]: expects string path, got %T", i, e)
		}
	}
	return nil
}

func (galleryKind) IsEmpty(v any) bool {
	arr, ok := v.([]any)
	return !ok || len(arr) == 0
}

func (galleryKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}

func (galleryKind) Class() Class       { return ClassField }
func (galleryKind) QueryOps() QueryOps { return QueryOps{} }

# gcmv2 代码约束

## 硬性规则（违反即重写）

### 1. 不在 if 里用长语句
绝不写 `if err := 很长(...); err != nil {` 这种长 if 头。
拆成变量声明 + 独立条件：

```go
// ❌ 不要
if _, err := s.db.Insert("auth_methods", x).Exec(); err != nil {
    return err
}

// ✅ 要
_, err := s.db.Insert("auth_methods", x).Exec()
if err != nil {
    return err
}
```

### 2. 讨论优先，不擅自改
`为什么 X？` / `你觉得呢？` / `先讨论`——是**问询/讨论**，不是让我改。
先回答/讨论清楚，用户点头**才动手**。不要自作主张改代码。

### 3. 不 diff 大改动不做确认就重构
涉及结构/接口/表变更——先对齐方案再实现。

## 偏好

- 简单直接、最低魔法（fail-loud 哲学）
- 新类型同类放一起、独立文件（每 kind 一个文件）
- 用 cast 容错取值（字段类型多变——不裸断言）

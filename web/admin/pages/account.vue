<template>
    <div style="max-width:420px;">
        <h3 style="margin-top:0;">账号</h3>
        <el-card>
            <template #header>
                <span>修改密码</span>
            </template>
            <el-form label-width="80px">
                <el-form-item label="当前密码">
                    <el-input v-model="form.old_password" type="password" show-password />
                </el-form-item>
                <el-form-item label="新密码">
                    <el-input v-model="form.new_password" type="password" show-password />
                </el-form-item>
                <el-form-item label="确认新密">
                    <el-input v-model="form.confirm" type="password" show-password />
                </el-form-item>
                <el-form-item>
                    <el-button type="primary" :loading="saving" @click="doChange">修改密码</el-button>
                </el-form-item>
            </el-form>
            <div style="color:#999;font-size:12px;line-height:1.8;">
                站点: {{ user?.site }} · 用户: {{ user?.username }}<br>
                新密码至少 8 位。修改后所有会话继续有效 (session 密钥不变)。
            </div>
        </el-card>
    </div>
</template>
<script>
import { reactive, ref, inject } from 'vue'
import $api from '$api'

export default {
    setup() {
        const form = reactive({ old_password: '', new_password: '', confirm: '' })
        const saving = ref(false)

        async function doChange() {
            if (form.new_password.length < 8) { ElMessage.error('新密码至少 8 位'); return }
            if (form.new_password !== form.confirm) { ElMessage.error('两次输入不一致'); return }
            saving.value = true
            try {
                await $api.changePassword(form.old_password, form.new_password)
                ElMessage.success('密码已修改')
                form.old_password = form.new_password = form.confirm = ''
            } catch (_) {}
            finally { saving.value = false }
        }

        return { user: inject('user'), form, saving, doChange }
    }
}
</script>

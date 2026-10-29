import { createApp } from 'vue'
import { createPinia } from 'pinia'
import '@xterm/xterm/css/xterm.css'
import './styles/fonts.css'
import './styles/themes/mint-y.css'
import './styles/base.css'
import App from './App.vue'
import { applyLook, rememberedLook } from './lib/theme'

// The look this browser last showed, until the settings say (D77).
applyLook(rememberedLook())

createApp(App).use(createPinia()).mount('#app')

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import '@xterm/xterm/css/xterm.css'
import './styles/fonts.css'
import './styles/themes/mint-y-dark.css'
import './styles/base.css'
import App from './App.vue'

createApp(App).use(createPinia()).mount('#app')

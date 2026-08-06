import { createApp } from 'vue'
import {
  Button,
  Collapse,
  ConfigProvider,
  Divider,
  Empty,
  Input,
  InputNumber,
  List,
  Modal,
  Popconfirm,
  Select,
  Segmented,
  Space,
  Tabs,
  Tag,
  Timeline,
  message,
} from 'ant-design-vue'
import 'ant-design-vue/dist/reset.css'
import './style.css'
import App from './App.vue'

const app = createApp(App)

app.use(Button)
app.use(Collapse)
app.use(ConfigProvider)
app.use(Divider)
app.use(Empty)
app.use(Input)
app.use(InputNumber)
app.use(List)
app.use(Modal)
app.use(Popconfirm)
app.use(Select)
app.use(Segmented)
app.use(Space)
app.use(Tabs)
app.use(Tag)
app.use(Timeline)
app.config.globalProperties.$message = message
app.mount('#app')

// UiDataTable 的列定义。独立成文件：<script setup> 内不允许 export 语句。
export interface DataTableColumn<T> {
  key: string
  label: string
  align?: 'left' | 'right' | 'center'
  sortable?: boolean
  /** 排序取值函数；提供后该列在组件内完成排序。 */
  value?: (row: T) => string | number
  width?: string
}

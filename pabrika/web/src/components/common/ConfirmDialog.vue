<script setup lang="ts">
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

withDefaults(
  defineProps<{ title: string; description?: string; confirmLabel?: string; destructive?: boolean; pending?: boolean }>(),
  { confirmLabel: 'Confirm', destructive: false, pending: false },
)
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ confirm: [] }>()
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription v-if="description">{{ description }}</DialogDescription>
      </DialogHeader>
      <slot />
      <DialogFooter>
        <Button variant="outline" :disabled="pending" @click="open = false">Cancel</Button>
        <Button :variant="destructive ? 'destructive' : 'default'" :disabled="pending" @click="emit('confirm')">
          {{ confirmLabel }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

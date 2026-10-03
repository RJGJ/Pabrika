<script setup lang="ts">
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { useBoardStore } from '@/stores/board'

const board = useBoardStore()

function dismiss(): void {
  board.acknowledgeLostAccess()
}
</script>

<template>
  <Dialog :open="board.lostAccess" @update:open="(v: boolean) => !v && dismiss()">
    <DialogContent :show-close-button="false" @interact-outside.prevent>
      <DialogHeader>
        <DialogTitle>You no longer have access to this project</DialogTitle>
        <DialogDescription>
          You were removed from it, or it was deleted. It has been taken off your project list.
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button @click="dismiss">Back to projects</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

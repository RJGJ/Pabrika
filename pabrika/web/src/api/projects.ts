import { api } from './client'
import type { CreateProjectInput, Envelope, Project, ProjectDetail, UpdateProjectInput } from './types'

export const projects = {
  list: async (archived = false, signal?: AbortSignal) =>
    (await api.get<Envelope<Project>>('/projects', { query: { archived: archived ? true : undefined }, signal })).items,
  create: (input: CreateProjectInput) => api.post<Project>('/projects', input),
  get: (keyOrId: string, signal?: AbortSignal) =>
    api.get<ProjectDetail>(`/projects/${encodeURIComponent(keyOrId)}`, { signal }),
  update: (keyOrId: string, patch: UpdateProjectInput) =>
    api.patch<Project>(`/projects/${encodeURIComponent(keyOrId)}`, patch),
  remove: (keyOrId: string) => api.delete(`/projects/${encodeURIComponent(keyOrId)}`),
}

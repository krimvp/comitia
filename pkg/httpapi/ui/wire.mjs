// The inspector keeps its existing model and accepts both HTTP wire generations.
// Translate only known records; envelope payloads and resume metadata are opaque.
const fields = {
  id:'ID', name:'Name', context_id:'ContextID', workspace_id:'WorkspaceID',
  parent_context_id:'ParentContextID', task_id:'TaskID', creator_session_id:'CreatorSessionID',
  parent_readable:'ParentReadable', created_at:'CreatedAt', archived:'Archived', archived_at:'ArchivedAt',
  session_id:'SessionID', principal_id:'PrincipalID', expires_at:'ExpiresAt',
  dedupe_key:'DedupeKey', body:'Body', sequence:'Sequence', title:'Title', status:'Status',
  lease_owner_session_id:'LeaseOwnerSessionID', lease_version:'LeaseVersion',
  lease_expires_at:'LeaseExpiresAt', updated_at:'UpdatedAt'
};
function record(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [fields[key] || key, item]));
}
export function normalizeResponse(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  const result = {...value};
  for (const key of ['contexts', 'participants', 'tasks', 'messages']) {
    if (Array.isArray(result[key])) result[key] = result[key].map(record);
  }
  for (const key of ['context', 'task', 'message']) {
    if (result[key]) result[key] = record(result[key]);
  }
  if (result.page && Array.isArray(result.page.messages)) {
    result.page = {...result.page, messages:result.page.messages.map(record)};
  }
  if (result.task_page && Array.isArray(result.task_page.tasks)) {
    result.task_page = {...result.task_page, tasks:result.task_page.tasks.map(record)};
  }
  if (result.participant_page && Array.isArray(result.participant_page.participants)) {
    result.participant_page = {...result.participant_page, participants:result.participant_page.participants.map(record)};
  }
  return result;
}

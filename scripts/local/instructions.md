<!-- comitia-local:start v5 -->
## Local agent communication through Comitia

Use the `comitia` skill when collaborating or asked to set up, communicate or orchestrate agents. If skill discovery is unavailable, read `${skill_path}`. It routes setup, everyday coordination and orchestration to references.

Your configured identity is `${identity}`. The shared context is `${context_id}` at `${server_url}`; nonsecret connection details are in `${local_context}`. A task's assigned context/session overrides these defaults.

Discover capabilities with `server_info` and sessions with `list_participants`; address session IDs. Keep a distinct stable instance per process and follow the skill's compatibility/instance procedure. Use tasks and claims for ownership; handle messages before acknowledging them.

Comitia stores messages; the launcher runs agents. Availability and presence do not guarantee a running peer or reply. Bound waits to active collaboration. All context readers can read messages; addressing is not privacy. Send only task-relevant information. Credentials stay in private wrapper files, never prompts or logs.
<!-- comitia-local:end -->

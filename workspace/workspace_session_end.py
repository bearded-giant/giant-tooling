#!/usr/bin/env python3
"""
Workspace Session End Hook for Claude Code
Hook: SessionEnd

Extracts session summary, discoveries, and plans from transcript.
Creates individual session files for grep-ability and git history.

Input (JSON on stdin):
{
    "session_id": "...",
    "cwd": "/current/working/directory",
    "transcript_path": "~/.claude/projects/.../session.jsonl"
}

Output files:
- scratch/history/sessions/{timestamp}_{session_id}.md  (detailed session file)
- scratch/history/sessions.md  (index with one-liners)
- scratch/context/discoveries.md  (appended)
- scratch/plans/current.md  (updated if plans found)

NOTE: Uses only Python standard library (no external dependencies)
"""

import sys
import json
import os
import re
from pathlib import Path
from datetime import datetime
from typing import List, Tuple, Dict, Set, Optional
from collections import defaultdict

# discovery categories to look for (non-capturing groups for findall)
DISCOVERY_PATTERNS = [
    (r'\b(?:discovered|found|learned|realized|noticed)\b.{10,100}', 'finding'),
    (r'\b(?:pattern|architecture|structure)\b.{10,100}', 'architecture'),
    (r'\b(?:gotcha|caveat|watch out|careful|note that|important)\b.{10,100}', 'gotcha'),
    (r'\b(?:convention|standard|style|naming)\b.{10,100}', 'convention'),
    (r'\b(?:dependency|requires|depends on|imports?)\b.{10,100}', 'dependency'),
    (r'\b(?:config|configuration|setting|environment)\b.{10,100}', 'config'),
    (r'\b(?:entry\s*point|main|bootstrap|init)\b.{10,100}', 'entry'),
]

# topic extraction keywords (weighted)
TOPIC_KEYWORDS = {
    'auth': ['auth', 'login', 'jwt', 'token', 'session', 'password', 'credential'],
    'api': ['api', 'endpoint', 'route', 'rest', 'graphql', 'request', 'response'],
    'database': ['database', 'sql', 'query', 'migration', 'model', 'schema', 'table'],
    'test': ['test', 'spec', 'pytest', 'jest', 'coverage', 'mock', 'fixture'],
    'bug': ['bug', 'fix', 'error', 'issue', 'debug', 'broken', 'failing'],
    'feature': ['feature', 'implement', 'add', 'create', 'new', 'build'],
    'refactor': ['refactor', 'cleanup', 'reorganize', 'restructure', 'rename'],
    'config': ['config', 'setting', 'env', 'environment', 'setup', 'install'],
    'docs': ['document', 'readme', 'comment', 'explain', 'describe'],
    'perf': ['performance', 'optimize', 'speed', 'slow', 'fast', 'cache'],
    'ui': ['ui', 'frontend', 'component', 'style', 'css', 'render', 'display'],
    'deploy': ['deploy', 'ci', 'cd', 'pipeline', 'docker', 'kubernetes'],
}


def read_transcript(transcript_path: str) -> List[dict]:
    """Read and parse the JSONL transcript file."""
    messages = []
    full_path = os.path.expanduser(transcript_path)

    if not os.path.exists(full_path):
        return messages

    try:
        with open(full_path, 'r') as f:
            for line in f:
                line = line.strip()
                if line:
                    try:
                        msg = json.loads(line)
                        messages.append(msg)
                    except json.JSONDecodeError:
                        continue
    except Exception:
        pass

    return messages


def extract_user_prompts(messages: List[dict]) -> List[str]:
    """Extract user prompts from transcript."""
    prompts = []
    for msg in messages:
        if msg.get('type') == 'user':
            message = msg.get('message', {})
            content = message.get('content', '')
            if isinstance(content, str) and content.strip():
                # truncate long prompts
                text = content.strip()
                if len(text) > 200:
                    text = text[:200] + '...'
                prompts.append(text)
            elif isinstance(content, list):
                for block in content:
                    if isinstance(block, dict) and block.get('type') == 'text':
                        text = block.get('text', '').strip()
                        if text:
                            if len(text) > 200:
                                text = text[:200] + '...'
                            prompts.append(text)
    return prompts


def extract_assistant_content(messages: List[dict]) -> str:
    """Extract text content from assistant messages."""
    content_parts = []

    for msg in messages:
        if msg.get('type') == 'assistant':
            message = msg.get('message', {})
            content = message.get('content', [])

            for block in content:
                if isinstance(block, dict) and block.get('type') == 'text':
                    content_parts.append(block.get('text', ''))
                elif isinstance(block, str):
                    content_parts.append(block)

    return '\n'.join(content_parts)


def extract_tool_usage(messages: List[dict]) -> Dict[str, List[str]]:
    """
    Extract tool usage with file paths from transcript.
    Returns dict: tool_name -> list of file paths touched.
    """
    tool_files: Dict[str, Set[str]] = defaultdict(set)

    for msg in messages:
        if msg.get('type') == 'assistant':
            message = msg.get('message', {})
            content = message.get('content', [])

            for block in content:
                if isinstance(block, dict) and block.get('type') == 'tool_use':
                    tool_name = block.get('name', 'unknown')
                    tool_input = block.get('input', {})

                    # extract file paths based on tool type
                    file_path = None
                    if tool_name in ('Read', 'Write', 'Edit', 'MultiEdit'):
                        file_path = tool_input.get('file_path')
                    elif tool_name == 'Glob':
                        file_path = tool_input.get('pattern')
                    elif tool_name == 'Grep':
                        file_path = tool_input.get('path') or tool_input.get('pattern')
                    elif tool_name == 'Bash':
                        cmd = tool_input.get('command', '')
                        if cmd:
                            # truncate long commands
                            file_path = cmd[:100] + ('...' if len(cmd) > 100 else '')
                    elif tool_name == 'Task':
                        desc = tool_input.get('description', '')
                        if desc:
                            file_path = f"[{desc}]"

                    if file_path:
                        tool_files[tool_name].add(file_path)
                    else:
                        # still count the tool use
                        tool_files[tool_name].add('')

    # convert sets to sorted lists, filter empty strings
    return {
        tool: sorted([f for f in files if f])
        for tool, files in tool_files.items()
    }


def extract_session_topic(user_prompts: List[str], assistant_content: str) -> str:
    """
    Extract a topic/theme from the session by analyzing content.
    Returns a short topic tag like 'auth', 'api', 'refactor'.
    """
    # combine all text for analysis
    all_text = ' '.join(user_prompts).lower() + ' ' + assistant_content.lower()

    # count keyword matches per topic
    topic_scores: Dict[str, int] = defaultdict(int)
    for topic, keywords in TOPIC_KEYWORDS.items():
        for keyword in keywords:
            count = len(re.findall(r'\b' + keyword + r'\w*\b', all_text))
            topic_scores[topic] += count

    # get top topic
    if topic_scores:
        top_topic = max(topic_scores.items(), key=lambda x: x[1])
        if top_topic[1] > 2:  # minimum threshold
            return top_topic[0]

    return 'general'


def extract_session_brief(user_prompts: List[str], topic: str) -> str:
    """
    Generate a brief summary from user prompts.
    Tries to capture the main intent of the session.
    """
    if not user_prompts:
        return f"{topic} session"

    # use first substantive prompt as base
    first_prompt = user_prompts[0]

    # clean it up for a brief
    brief = first_prompt.replace('\n', ' ').strip()

    # if it's a question, keep it short
    if '?' in brief:
        brief = brief.split('?')[0] + '?'

    # truncate
    if len(brief) > 80:
        brief = brief[:77] + '...'

    return brief


def extract_timestamps(messages: List[dict]) -> Tuple[Optional[datetime], Optional[datetime]]:
    """Extract session start and end timestamps from messages."""
    start_time = None
    end_time = None

    for msg in messages:
        ts = msg.get('timestamp')
        if ts:
            try:
                dt = datetime.fromisoformat(ts.replace('Z', '+00:00'))
                if start_time is None or dt < start_time:
                    start_time = dt
                if end_time is None or dt > end_time:
                    end_time = dt
            except (ValueError, AttributeError):
                pass

    return start_time, end_time


def extract_discoveries(content: str) -> List[Tuple[str, str]]:
    """Extract potential discoveries from assistant content."""
    discoveries = []
    seen = set()

    for pattern, category in DISCOVERY_PATTERNS:
        matches = re.findall(pattern, content, re.IGNORECASE | re.MULTILINE)
        for match in matches:
            if isinstance(match, tuple):
                match = ' '.join(match)

            finding = match.strip()
            if len(finding) < 20 or finding in seen:
                continue

            if len(finding) > 200:
                finding = finding[:200] + '...'

            seen.add(finding)
            discoveries.append((category, finding))

    return discoveries[:10]


def extract_plans(content: str) -> List[str]:
    """Extract implementation plans/steps from content."""
    plans = []
    seen = set()

    list_pattern = r'(?:^|\n)\s*(\d+[\.\)]\s+.+?)(?=\n\s*\d+[\.\)]|\n\n|$)'
    matches = re.findall(list_pattern, content, re.MULTILINE | re.DOTALL)

    for match in matches:
        step = match.strip()
        if len(step) > 15 and step not in seen:
            seen.add(step)
            plans.append(step)

    todo_pattern = r'\b(?:TODO|NEXT|STEP)\s*[:\-]?\s*(.+?)(?=\n|$)'
    matches = re.findall(todo_pattern, content, re.IGNORECASE)

    for match in matches:
        step = match.strip()
        if len(step) > 10 and step not in seen:
            seen.add(step)
            plans.append(f"TODO: {step}")

    return plans[:15]


def create_session_file(
    scratch_dir: Path,
    session_id: str,
    start_time: Optional[datetime],
    end_time: Optional[datetime],
    topic: str,
    brief: str,
    user_prompts: List[str],
    tool_usage: Dict[str, List[str]],
    discoveries: List[Tuple[str, str]],
) -> Optional[Path]:
    """Create individual session summary file."""
    sessions_dir = scratch_dir / "history" / "sessions"
    sessions_dir.mkdir(parents=True, exist_ok=True)

    # filename: YYYYMMDD_HHMMSS_sessionid.md
    now = datetime.now()
    timestamp_str = now.strftime('%Y%m%d_%H%M%S')
    session_short = session_id[:8] if session_id else 'unknown'
    filename = f"{timestamp_str}_{session_short}.md"
    session_file = sessions_dir / filename

    # format times
    start_str = start_time.strftime('%Y-%m-%d %H:%M') if start_time else now.strftime('%Y-%m-%d %H:%M')
    end_str = end_time.strftime('%H:%M') if end_time else now.strftime('%H:%M')

    # build content
    lines = [
        f"# Session: {start_str} - {end_str}",
        "",
        "## Summary",
        f"Topic: {topic}",
        f"Brief: {brief}",
        "",
    ]

    # user prompts
    if user_prompts:
        lines.append("## User Prompts")
        for prompt in user_prompts[:10]:  # limit to 10
            # escape for markdown
            prompt_clean = prompt.replace('\n', ' ').strip()
            lines.append(f"- {prompt_clean}")
        lines.append("")

    # files modified (group by action)
    if tool_usage:
        lines.append("## Files Touched")

        # group by modification type
        modified = tool_usage.get('Edit', []) + tool_usage.get('MultiEdit', [])
        created = tool_usage.get('Write', [])
        read_files = tool_usage.get('Read', [])

        if modified:
            lines.append("### Modified")
            for f in sorted(set(modified))[:20]:
                lines.append(f"- {f}")

        if created:
            lines.append("### Created")
            for f in sorted(set(created))[:10]:
                lines.append(f"- {f}")

        if read_files:
            lines.append("### Read")
            for f in sorted(set(read_files))[:15]:
                lines.append(f"- {f}")

        lines.append("")

    # tool usage stats
    if tool_usage:
        lines.append("## Tool Usage")
        for tool, files in sorted(tool_usage.items()):
            count = len(files) if files else 1
            lines.append(f"- {tool}: {count}")
        lines.append("")

    # bash commands
    bash_cmds = tool_usage.get('Bash', [])
    if bash_cmds:
        lines.append("## Commands Run")
        for cmd in bash_cmds[:10]:
            lines.append(f"- `{cmd}`")
        lines.append("")

    # discoveries
    if discoveries:
        lines.append("## Discoveries Extracted")
        for category, finding in discoveries:
            finding_clean = finding.replace('\n', ' ').strip()
            lines.append(f"- [{category}] {finding_clean}")
        lines.append("")

    # session metadata
    lines.extend([
        "## Metadata",
        f"- Session ID: {session_id}",
        f"- Generated: {now.strftime('%Y-%m-%d %H:%M:%S')}",
    ])

    try:
        session_file.write_text('\n'.join(lines))
        return session_file
    except Exception:
        return None


def update_session_index(
    scratch_dir: Path,
    session_id: str,
    topic: str,
    brief: str,
    tool_usage: Dict[str, List[str]],
    discoveries_count: int,
    session_filename: str,
):
    """Add one-liner to sessions.md index."""
    index_file = scratch_dir / "history" / "sessions.md"
    index_file.parent.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M')
    session_short = session_id[:8] if session_id else 'unknown'

    # count edits
    edit_count = len(tool_usage.get('Edit', [])) + len(tool_usage.get('Write', []))

    # build summary parts
    parts = []
    if edit_count > 0:
        parts.append(f"{edit_count} edits")
    if discoveries_count > 0:
        parts.append(f"{discoveries_count} discoveries")

    summary = ', '.join(parts) if parts else 'read-only'

    # truncate brief for index
    brief_short = brief[:50] + '...' if len(brief) > 50 else brief

    line = f"- {timestamp}: [{topic}] {session_short} - {brief_short} ({summary})"

    try:
        with open(index_file, 'a') as f:
            f.write(line + '\n')
    except Exception:
        pass


def append_discoveries(scratch_dir: Path, discoveries: List[Tuple[str, str]]) -> int:
    """Append discoveries to discoveries.md."""
    if not discoveries:
        return 0

    discoveries_file = scratch_dir / "context" / "discoveries.md"
    discoveries_file.parent.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M')

    lines = []
    for category, finding in discoveries:
        finding = finding.replace('\n', ' ').strip()
        lines.append(f"- {timestamp}: [{category}] {finding}")

    try:
        with open(discoveries_file, 'a') as f:
            f.write('\n'.join(lines) + '\n')
        return len(lines)
    except Exception:
        return 0


def save_plans(scratch_dir: Path, plans: List[str]) -> bool:
    """Save plans to plans/current.md."""
    if not plans:
        return False

    plans_file = scratch_dir / "plans" / "current.md"
    plans_file.parent.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M')

    content = f"# Current Plan\nUpdated: {timestamp}\n\n"
    content += "## Steps\n"
    for i, plan in enumerate(plans, 1):
        plan = plan.replace('\n', ' ').strip()
        if not plan.startswith(('TODO', 'FIXME', 'NEXT')):
            content += f"{i}. {plan}\n"
        else:
            content += f"- {plan}\n"

    try:
        if plans_file.exists():
            mtime = plans_file.stat().st_mtime
            age_hours = (datetime.now().timestamp() - mtime) / 3600
            if age_hours < 1:
                with open(plans_file, 'a') as f:
                    f.write(f"\n---\n{content}")
                return True

        with open(plans_file, 'w') as f:
            f.write(content)
        return True
    except Exception:
        return False


def main():
    """Main hook entry point."""
    try:
        input_data = json.load(sys.stdin)

        session_id = input_data.get("session_id", "unknown")
        cwd = input_data.get("cwd", os.getcwd())
        transcript_path = input_data.get("transcript_path", "")

        scratch_dir = Path(cwd) / "scratch"

        if not scratch_dir.exists():
            return

        if not transcript_path:
            return

        messages = read_transcript(transcript_path)
        if not messages:
            return

        # extract all content
        user_prompts = extract_user_prompts(messages)
        assistant_content = extract_assistant_content(messages)
        tool_usage = extract_tool_usage(messages)
        start_time, end_time = extract_timestamps(messages)

        if not assistant_content and not user_prompts:
            return

        # derive topic and brief
        topic = extract_session_topic(user_prompts, assistant_content)
        brief = extract_session_brief(user_prompts, topic)

        # extract discoveries and plans
        discoveries = extract_discoveries(assistant_content)
        plans = extract_plans(assistant_content)

        # create individual session file
        session_file = create_session_file(
            scratch_dir=scratch_dir,
            session_id=session_id,
            start_time=start_time,
            end_time=end_time,
            topic=topic,
            brief=brief,
            user_prompts=user_prompts,
            tool_usage=tool_usage,
            discoveries=discoveries,
        )

        # update index
        session_filename = session_file.name if session_file else ''
        update_session_index(
            scratch_dir=scratch_dir,
            session_id=session_id,
            topic=topic,
            brief=brief,
            tool_usage=tool_usage,
            discoveries_count=len(discoveries),
            session_filename=session_filename,
        )

        # persist discoveries and plans (existing behavior)
        discoveries_count = append_discoveries(scratch_dir, discoveries)
        has_plans = save_plans(scratch_dir, plans)

        # output summary
        parts = []
        if session_file:
            parts.append(f"session:{session_file.name}")
        if discoveries_count > 0:
            parts.append(f"{discoveries_count} discoveries")
        if has_plans:
            parts.append("plans")

        if parts:
            print(f"Workspace: {', '.join(parts)}", file=sys.stderr)

    except Exception:
        pass


if __name__ == "__main__":
    main()

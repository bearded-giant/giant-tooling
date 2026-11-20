import os
import subprocess
import sys

import pytest

sys.path.insert(0, os.path.dirname(__file__))
import backfill_frontmatter
import merge_delta_spec
import migrate_spec_to_proposal

MODULES = [backfill_frontmatter, merge_delta_spec, migrate_spec_to_proposal]
GIT = ["git", "-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"]


def _git(cwd, *args):
    subprocess.run([*GIT, "-C", str(cwd), *args], check=True, capture_output=True)


@pytest.fixture
def layout(tmp_path):
    src = tmp_path / "src"
    _git(tmp_path, "init", "-q", str(src))
    _git(src, "commit", "-q", "--allow-empty", "-m", "init")
    base = tmp_path / "proj-wt"
    _git(tmp_path, "clone", "-q", "--bare", str(src), str(base / ".bare"))
    _git(base / ".bare", "worktree", "add", "-q", "-b", "feat-x", "../feat-x", "main")
    return src, base / "feat-x"


@pytest.mark.parametrize("mod", MODULES, ids=lambda m: m.__name__)
def test_bare_worktree_repo_is_container(layout, mod):
    _, wt = layout
    assert mod.detect_repo(wt / ".giantmem") == ("proj-wt", "feat-x")


@pytest.mark.parametrize("mod", MODULES, ids=lambda m: m.__name__)
def test_plain_repo_keeps_dir_name(layout, mod):
    src, _ = layout
    assert mod.detect_repo(src / ".giantmem") == ("src", "main")

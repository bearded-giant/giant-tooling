export namespace artifacts {
	
	export class Artifact {
	    id: string;
	    type: string;
	    feature?: string;
	    domain?: string;
	    name?: string;
	    status: string;
	    path: string;
	    repo: string;
	    branch: string;
	    worktree?: string;
	    size: number;
	    updated: string;
	    created?: string;
	    has_frontmatter: boolean;
	    scope?: string;
	    lifecycle?: string;
	    access_count?: number;
	    has_vec?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Artifact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.feature = source["feature"];
	        this.domain = source["domain"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.path = source["path"];
	        this.repo = source["repo"];
	        this.branch = source["branch"];
	        this.worktree = source["worktree"];
	        this.size = source["size"];
	        this.updated = source["updated"];
	        this.created = source["created"];
	        this.has_frontmatter = source["has_frontmatter"];
	        this.scope = source["scope"];
	        this.lifecycle = source["lifecycle"];
	        this.access_count = source["access_count"];
	        this.has_vec = source["has_vec"];
	    }
	}
	export class ListFilter {
	    Type: string[];
	    Status: string[];
	    Lifecycle: string[];
	    Scope: string;
	    Repo: string;
	    Branch: string;
	    Feature: string;
	    Domain: string;
	
	    static createFrom(source: any = {}) {
	        return new ListFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Type = source["Type"];
	        this.Status = source["Status"];
	        this.Lifecycle = source["Lifecycle"];
	        this.Scope = source["Scope"];
	        this.Repo = source["Repo"];
	        this.Branch = source["Branch"];
	        this.Feature = source["Feature"];
	        this.Domain = source["Domain"];
	    }
	}

}

export namespace main {
	
	export class ActivityCounts {
	    liveDocs: number;
	    sessions: number;
	    writesToday: number;
	    activeFeatures: number;
	
	    static createFrom(source: any = {}) {
	        return new ActivityCounts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.liveDocs = source["liveDocs"];
	        this.sessions = source["sessions"];
	        this.writesToday = source["writesToday"];
	        this.activeFeatures = source["activeFeatures"];
	    }
	}
	export class BrowseRow {
	    path: string;
	    rel: string;
	    repo: string;
	    feature: string;
	    worktree: string;
	    type: string;
	    mtime: number;
	    sessionId: string;
	    dead: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BrowseRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.rel = source["rel"];
	        this.repo = source["repo"];
	        this.feature = source["feature"];
	        this.worktree = source["worktree"];
	        this.type = source["type"];
	        this.mtime = source["mtime"];
	        this.sessionId = source["sessionId"];
	        this.dead = source["dead"];
	    }
	}
	export class FacetCountsResult {
	    byType: Record<string, number>;
	    byLifecycle: Record<string, number>;
	    byStatus: Record<string, number>;
	    byFeature: Record<string, number>;
	    byRepo: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new FacetCountsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.byType = source["byType"];
	        this.byLifecycle = source["byLifecycle"];
	        this.byStatus = source["byStatus"];
	        this.byFeature = source["byFeature"];
	        this.byRepo = source["byRepo"];
	    }
	}
	export class FeatureRow {
	    repo: string;
	    feature: string;
	    count: number;
	    worktree?: string;
	
	    static createFrom(source: any = {}) {
	        return new FeatureRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repo = source["repo"];
	        this.feature = source["feature"];
	        this.count = source["count"];
	        this.worktree = source["worktree"];
	    }
	}
	export class FileActivity {
	    path: string;
	    project: string;
	    feature?: string;
	    dirType: string;
	    mtime: number;
	
	    static createFrom(source: any = {}) {
	        return new FileActivity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.project = source["project"];
	        this.feature = source["feature"];
	        this.dirType = source["dirType"];
	        this.mtime = source["mtime"];
	    }
	}
	export class HeatmapCell {
	    worktreePath: string;
	    project: string;
	    day: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new HeatmapCell(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.worktreePath = source["worktreePath"];
	        this.project = source["project"];
	        this.day = source["day"];
	        this.count = source["count"];
	    }
	}
	export class RepoActivity {
	    project: string;
	    worktreePath: string;
	    docCount: number;
	    mtime: number;
	
	    static createFrom(source: any = {}) {
	        return new RepoActivity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.worktreePath = source["worktreePath"];
	        this.docCount = source["docCount"];
	        this.mtime = source["mtime"];
	    }
	}
	export class SessionFacetCounts {
	    byProject: Record<string, number>;
	    byDirType: Record<string, number>;
	    byTopic: Record<string, number>;
	    byDate: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new SessionFacetCounts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.byProject = source["byProject"];
	        this.byDirType = source["byDirType"];
	        this.byTopic = source["byTopic"];
	        this.byDate = source["byDate"];
	    }
	}
	export class SessionFilter {
	    project?: string;
	    dirType?: string;
	    topic?: string;
	    since?: string;
	    until?: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.dirType = source["dirType"];
	        this.topic = source["topic"];
	        this.since = source["since"];
	        this.until = source["until"];
	    }
	}
	export class SparklinePoint {
	    day: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new SparklinePoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.day = source["day"];
	        this.count = source["count"];
	    }
	}
	export class ToolUseFilter {
	    query?: string;
	    toolName?: string;
	    project?: string;
	    useFTSPre?: boolean;
	    since?: string;
	    until?: string;
	    limit?: number;
	
	    static createFrom(source: any = {}) {
	        return new ToolUseFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.query = source["query"];
	        this.toolName = source["toolName"];
	        this.project = source["project"];
	        this.useFTSPre = source["useFTSPre"];
	        this.since = source["since"];
	        this.until = source["until"];
	        this.limit = source["limit"];
	    }
	}
	export class ToolUseHit {
	    sessionPath: string;
	    sessionId: string;
	    project?: string;
	    timestamp?: string;
	    turnIndex: number;
	    toolName: string;
	    inputSummary: string;
	    inputJSON: string;
	    output?: string;
	    outputClip?: string;
	    isError?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolUseHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionPath = source["sessionPath"];
	        this.sessionId = source["sessionId"];
	        this.project = source["project"];
	        this.timestamp = source["timestamp"];
	        this.turnIndex = source["turnIndex"];
	        this.toolName = source["toolName"];
	        this.inputSummary = source["inputSummary"];
	        this.inputJSON = source["inputJSON"];
	        this.output = source["output"];
	        this.outputClip = source["outputClip"];
	        this.isError = source["isError"];
	    }
	}

}

export namespace project {
	
	export class Deleted {
	    liveDocs: number;
	    artifacts: number;
	    embeddings: number;
	    accessRows: number;
	    sessions: number;
	    archiveDocs: number;
	
	    static createFrom(source: any = {}) {
	        return new Deleted(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.liveDocs = source["liveDocs"];
	        this.artifacts = source["artifacts"];
	        this.embeddings = source["embeddings"];
	        this.accessRows = source["accessRows"];
	        this.sessions = source["sessions"];
	        this.archiveDocs = source["archiveDocs"];
	    }
	}

}

export namespace search {
	
	export class Hit {
	    score: number;
	    source: string;
	    project: string;
	    timestamp?: string;
	    dir_type?: string;
	    feature?: string;
	    filepath: string;
	    filename: string;
	    is_latest?: boolean;
	    source_type?: string;
	    session_id?: string;
	    cwd?: string;
	    topic?: string;
	    snippet?: string;
	
	    static createFrom(source: any = {}) {
	        return new Hit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.score = source["score"];
	        this.source = source["source"];
	        this.project = source["project"];
	        this.timestamp = source["timestamp"];
	        this.dir_type = source["dir_type"];
	        this.feature = source["feature"];
	        this.filepath = source["filepath"];
	        this.filename = source["filename"];
	        this.is_latest = source["is_latest"];
	        this.source_type = source["source_type"];
	        this.session_id = source["session_id"];
	        this.cwd = source["cwd"];
	        this.topic = source["topic"];
	        this.snippet = source["snippet"];
	    }
	}
	export class HybridResult {
	    artifact: artifacts.Artifact;
	    score: number;
	    fts_score: number;
	    vector_score: number;
	    recency_score: number;
	    access_score: number;
	
	    static createFrom(source: any = {}) {
	        return new HybridResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.artifact = this.convertValues(source["artifact"], artifacts.Artifact);
	        this.score = source["score"];
	        this.fts_score = source["fts_score"];
	        this.vector_score = source["vector_score"];
	        this.recency_score = source["recency_score"];
	        this.access_score = source["access_score"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Params {
	    Query: string;
	    Project: string;
	    DirType: string;
	    SourceType: string;
	    Feature: string;
	    Latest: boolean;
	    LiveOnly: boolean;
	    ArchiveOnly: boolean;
	    Since: string;
	    Until: string;
	    Limit: number;
	    IncludeFull: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Params(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Query = source["Query"];
	        this.Project = source["Project"];
	        this.DirType = source["DirType"];
	        this.SourceType = source["SourceType"];
	        this.Feature = source["Feature"];
	        this.Latest = source["Latest"];
	        this.LiveOnly = source["LiveOnly"];
	        this.ArchiveOnly = source["ArchiveOnly"];
	        this.Since = source["Since"];
	        this.Until = source["Until"];
	        this.Limit = source["Limit"];
	        this.IncludeFull = source["IncludeFull"];
	    }
	}

}


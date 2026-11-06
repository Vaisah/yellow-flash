export namespace download {
	
	export class Download {
	    id: string;
	    url: string;
	    finalUrl: string;
	    filename: string;
	    filenameExplicit: boolean;
	    destination: string;
	    tempPath: string;
	    mimeType: string;
	    etag: string;
	    lastModified: string;
	    error: string;
	    size: number;
	    downloaded: number;
	    rangeSupported: boolean;
	    connections: number;
	    retries: number;
	    status: string;
	    queueId?: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    completedAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Download(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.url = source["url"];
	        this.finalUrl = source["finalUrl"];
	        this.filename = source["filename"];
	        this.filenameExplicit = source["filenameExplicit"];
	        this.destination = source["destination"];
	        this.tempPath = source["tempPath"];
	        this.mimeType = source["mimeType"];
	        this.etag = source["etag"];
	        this.lastModified = source["lastModified"];
	        this.error = source["error"];
	        this.size = source["size"];
	        this.downloaded = source["downloaded"];
	        this.rangeSupported = source["rangeSupported"];
	        this.connections = source["connections"];
	        this.retries = source["retries"];
	        this.status = source["status"];
	        this.queueId = source["queueId"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.completedAt = this.convertValues(source["completedAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
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
	export class LinkInfo {
	    url: string;
	    finalUrl: string;
	    filename: string;
	    size: number;
	    mimeType: string;
	    rangeSupported: boolean;
	    webPage: boolean;
	
	    static createFrom(source: any = {}) {
	        return new LinkInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.finalUrl = source["finalUrl"];
	        this.filename = source["filename"];
	        this.size = source["size"];
	        this.mimeType = source["mimeType"];
	        this.rangeSupported = source["rangeSupported"];
	        this.webPage = source["webPage"];
	    }
	}
	export class Segment {
	    id: string;
	    downloadId: string;
	    index: number;
	    start: number;
	    end: number;
	    downloaded: number;
	    status: string;
	    retries: number;
	
	    static createFrom(source: any = {}) {
	        return new Segment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.downloadId = source["downloadId"];
	        this.index = source["index"];
	        this.start = source["start"];
	        this.end = source["end"];
	        this.downloaded = source["downloaded"];
	        this.status = source["status"];
	        this.retries = source["retries"];
	    }
	}

}

export namespace main {
	
	export class AddRequest {
	    url: string;
	    directory: string;
	    filename: string;
	    connections: number;
	    start: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AddRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.directory = source["directory"];
	        this.filename = source["filename"];
	        this.connections = source["connections"];
	        this.start = source["start"];
	    }
	}
	export class AppInfo {
	    name: string;
	    version: string;
	    dataDir: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.dataDir = source["dataDir"];
	    }
	}
	export class Settings {
	    downloadDirectory: string;
	    connections: number;
	    startImmediately: boolean;
	    useClipboard: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.downloadDirectory = source["downloadDirectory"];
	        this.connections = source["connections"];
	        this.startImmediately = source["startImmediately"];
	        this.useClipboard = source["useClipboard"];
	    }
	}

}


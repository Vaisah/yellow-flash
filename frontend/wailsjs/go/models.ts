export namespace browser {
	
	export class BrowserCapture {
	    name: string;
	    capture: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BrowserCapture(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.capture = source["capture"];
	    }
	}
	export class CaptureRules {
	    advancedIntegration: boolean;
	    browsers: BrowserCapture[];
	    autoTypes: string;
	    excludedSites: string;
	    excludedAddresses: string[];
	    videoPanel: boolean;
	    videoPanelSites: string;
	    captureOpened: boolean;
	    captureMinKB: number;
	
	    static createFrom(source: any = {}) {
	        return new CaptureRules(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.advancedIntegration = source["advancedIntegration"];
	        this.browsers = this.convertValues(source["browsers"], BrowserCapture);
	        this.autoTypes = source["autoTypes"];
	        this.excludedSites = source["excludedSites"];
	        this.excludedAddresses = source["excludedAddresses"];
	        this.videoPanel = source["videoPanel"];
	        this.videoPanelSites = source["videoPanelSites"];
	        this.captureOpened = source["captureOpened"];
	        this.captureMinKB = source["captureMinKB"];
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
	export class Seen {
	    Browser: string;
	    Version: string;
	
	    static createFrom(source: any = {}) {
	        return new Seen(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Browser = source["Browser"];
	        this.Version = source["Version"];
	    }
	}

}

export namespace crash {
	
	export class Found {
	    number: number;
	    title: string;
	    html_url: string;
	    state: string;
	
	    static createFrom(source: any = {}) {
	        return new Found(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.number = source["number"];
	        this.title = source["title"];
	        this.html_url = source["html_url"];
	        this.state = source["state"];
	    }
	}
	export class Report {
	    id: string;
	    kind: string;
	    signature: string;
	    // Go type: time
	    time: any;
	    path: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.signature = source["signature"];
	        this.time = this.convertValues(source["time"], null);
	        this.path = source["path"];
	        this.text = source["text"];
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

}

export namespace debrid {
	
	export class AccountInfo {
	    user: string;
	    premium: boolean;
	    premiumLeft: number;
	
	    static createFrom(source: any = {}) {
	        return new AccountInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user = source["user"];
	        this.premium = source["premium"];
	        this.premiumLeft = source["premiumLeft"];
	    }
	}

}

export namespace download {
	
	export class ServerInfo {
	    ip?: string;
	    rttMs?: number;
	    proxied?: boolean;
	    cdn?: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ip = source["ip"];
	        this.rttMs = source["rttMs"];
	        this.proxied = source["proxied"];
	        this.cdn = source["cdn"];
	    }
	}
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
	    speedLimit: number;
	    expectedChecksum?: string;
	    restricted?: boolean;
	    server?: ServerInfo;
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
	        this.speedLimit = source["speedLimit"];
	        this.expectedChecksum = source["expectedChecksum"];
	        this.restricted = source["restricted"];
	        this.server = this.convertValues(source["server"], ServerInfo);
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
	    username?: string;
	    password?: string;
	
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
	        this.username = source["username"];
	        this.password = source["password"];
	    }
	}
	export class AddTorrentRequest {
	    hash: string;
	    directory: string;
	    files: number[];
	    paused: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AddTorrentRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.directory = source["directory"];
	        this.files = source["files"];
	        this.paused = source["paused"];
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
	export class BrowserIntegration {
	    host: string;
	    portable: boolean;
	    seen: browser.Seen[];
	
	    static createFrom(source: any = {}) {
	        return new BrowserIntegration(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.portable = source["portable"];
	        this.seen = this.convertValues(source["seen"], browser.Seen);
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
	export class CleanResult {
	    files: number;
	    bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new CleanResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.bytes = source["bytes"];
	    }
	}
	export class DebridAccount {
	    id: string;
	    service: string;
	    user: string;
	    enabled: boolean;
	    signedIn: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DebridAccount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.service = source["service"];
	        this.user = source["user"];
	        this.enabled = source["enabled"];
	        this.signedIn = source["signedIn"];
	    }
	}
	export class DebridService {
	    service: string;
	    name: string;
	    tokenPage: string;
	    signsInByCode: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DebridService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.service = source["service"];
	        this.name = source["name"];
	        this.tokenPage = source["tokenPage"];
	        this.signsInByCode = source["signsInByCode"];
	    }
	}
	export class DebridSettings {
	    accounts: DebridAccount[];
	    torrents: boolean;
	    keychain: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DebridSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accounts = this.convertValues(source["accounts"], DebridAccount);
	        this.torrents = source["torrents"];
	        this.keychain = source["keychain"];
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
	export class DebridSignIn {
	    service: string;
	    code: string;
	    page: string;
	    // Go type: time
	    expires: any;
	    state: string;
	    error?: string;
	    account?: DebridAccount;
	
	    static createFrom(source: any = {}) {
	        return new DebridSignIn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.service = source["service"];
	        this.code = source["code"];
	        this.page = source["page"];
	        this.expires = this.convertValues(source["expires"], null);
	        this.state = source["state"];
	        this.error = source["error"];
	        this.account = this.convertValues(source["account"], DebridAccount);
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
	export class DebridTorrent {
	    id: string;
	    name: string;
	    hash: string;
	    magnet: string;
	    account: string;
	    service: string;
	    transfer?: string;
	    dir: string;
	    path?: string;
	    state: string;
	    progress: number;
	    done: number;
	    total: number;
	    error?: string;
	    notes?: string[];
	    // Go type: time
	    added: any;
	
	    static createFrom(source: any = {}) {
	        return new DebridTorrent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.hash = source["hash"];
	        this.magnet = source["magnet"];
	        this.account = source["account"];
	        this.service = source["service"];
	        this.transfer = source["transfer"];
	        this.dir = source["dir"];
	        this.path = source["path"];
	        this.state = source["state"];
	        this.progress = source["progress"];
	        this.done = source["done"];
	        this.total = source["total"];
	        this.error = source["error"];
	        this.notes = source["notes"];
	        this.added = this.convertValues(source["added"], null);
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
	export class DiskInfo {
	    dir: string;
	    free: number;
	    total: number;
	    usedPercent: number;
	
	    static createFrom(source: any = {}) {
	        return new DiskInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dir = source["dir"];
	        this.free = source["free"];
	        this.total = source["total"];
	        this.usedPercent = source["usedPercent"];
	    }
	}
	export class ExtractSettings {
	    auto: boolean;
	    into: string;
	    deleteArchive: boolean;
	    sevenZip: string;
	    sevenZipFound: string;
	
	    static createFrom(source: any = {}) {
	        return new ExtractSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.auto = source["auto"];
	        this.into = source["into"];
	        this.deleteArchive = source["deleteArchive"];
	        this.sevenZip = source["sevenZip"];
	        this.sevenZipFound = source["sevenZipFound"];
	    }
	}
	export class FlagCell {
	    x: number;
	    y: number;
	    w: number;
	    h: number;
	
	    static createFrom(source: any = {}) {
	        return new FlagCell(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.x = source["x"];
	        this.y = source["y"];
	        this.w = source["w"];
	        this.h = source["h"];
	    }
	}
	export class GalleryRequest {
	    url: string;
	    directory: string;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new GalleryRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.directory = source["directory"];
	        this.limit = source["limit"];
	    }
	}
	export class HelperInfo {
	    name: string;
	    version: string;
	    exe: string;
	    chosen: boolean;
	    chosenTooOld?: string;
	
	    static createFrom(source: any = {}) {
	        return new HelperInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.exe = source["exe"];
	        this.chosen = source["chosen"];
	        this.chosenTooOld = source["chosenTooOld"];
	    }
	}
	export class MediaRow {
	    id: string;
	    kind: string;
	    title: string;
	    url: string;
	    progress: tools.VideoProgress;
	
	    static createFrom(source: any = {}) {
	        return new MediaRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.url = source["url"];
	        this.progress = this.convertValues(source["progress"], tools.VideoProgress);
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
	export class NetworkSettings {
	    speedLimitKBps: number;
	    dnsMode: string;
	    dnsPreset: string;
	    dnsPresetMode: string;
	    dnsProfile: string;
	    dnsServers: string;
	    dnsTlsName: string;
	    dnsUrl: string;
	    dnsFallback: boolean;
	    proxyMode: string;
	    proxyUrl: string;
	    pacUrl: string;
	    pacScript: string;
	    ftpActive: boolean;
	    dataCapEnabled: boolean;
	    dataCapMB: number;
	    dataCapHours: number;
	
	    static createFrom(source: any = {}) {
	        return new NetworkSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.speedLimitKBps = source["speedLimitKBps"];
	        this.dnsMode = source["dnsMode"];
	        this.dnsPreset = source["dnsPreset"];
	        this.dnsPresetMode = source["dnsPresetMode"];
	        this.dnsProfile = source["dnsProfile"];
	        this.dnsServers = source["dnsServers"];
	        this.dnsTlsName = source["dnsTlsName"];
	        this.dnsUrl = source["dnsUrl"];
	        this.dnsFallback = source["dnsFallback"];
	        this.proxyMode = source["proxyMode"];
	        this.proxyUrl = source["proxyUrl"];
	        this.pacUrl = source["pacUrl"];
	        this.pacScript = source["pacScript"];
	        this.ftpActive = source["ftpActive"];
	        this.dataCapEnabled = source["dataCapEnabled"];
	        this.dataCapMB = source["dataCapMB"];
	        this.dataCapHours = source["dataCapHours"];
	    }
	}
	export class Options {
	    launchOnStartup: boolean;
	    showComplete: boolean;
	    browserCapture: boolean;
	    fileTypes: string;
	
	    static createFrom(source: any = {}) {
	        return new Options(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.launchOnStartup = source["launchOnStartup"];
	        this.showComplete = source["showComplete"];
	        this.browserCapture = source["browserCapture"];
	        this.fileTypes = source["fileTypes"];
	    }
	}
	export class QueueView {
	    id: string;
	    name: string;
	    filesAtOnce: number;
	    items: string[];
	    sync?: boolean;
	    schedule: queue.Schedule;
	    // Go type: time
	    createdAt: any;
	    running: boolean;
	    builtin: boolean;
	
	    static createFrom(source: any = {}) {
	        return new QueueView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.filesAtOnce = source["filesAtOnce"];
	        this.items = source["items"];
	        this.sync = source["sync"];
	        this.schedule = this.convertValues(source["schedule"], queue.Schedule);
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.running = source["running"];
	        this.builtin = source["builtin"];
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
	export class RemoteInfo {
	    status: remote.Status;
	    urls: string[];
	    fingerprint: string;
	    devices: remote.Device[];
	
	    static createFrom(source: any = {}) {
	        return new RemoteInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], remote.Status);
	        this.urls = source["urls"];
	        this.fingerprint = source["fingerprint"];
	        this.devices = this.convertValues(source["devices"], remote.Device);
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
	export class RemotePairing {
	    code: string;
	    secondsLeft: number;
	    paired: string;
	    urls: string[];
	
	    static createFrom(source: any = {}) {
	        return new RemotePairing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.secondsLeft = source["secondsLeft"];
	        this.paired = source["paired"];
	        this.urls = source["urls"];
	    }
	}
	export class ServerLocation {
	    place: string;
	    ip?: string;
	    country?: string;
	    flag?: FlagCell;
	    rttMs?: number;
	    reach?: string;
	    proxied?: boolean;
	    cdn?: string;
	    attribution: string;
	    attributionUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerLocation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.place = source["place"];
	        this.ip = source["ip"];
	        this.country = source["country"];
	        this.flag = this.convertValues(source["flag"], FlagCell);
	        this.rttMs = source["rttMs"];
	        this.reach = source["reach"];
	        this.proxied = source["proxied"];
	        this.cdn = source["cdn"];
	        this.attribution = source["attribution"];
	        this.attributionUrl = source["attributionUrl"];
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
	export class SiteLogin {
	    site: string;
	    user: string;
	
	    static createFrom(source: any = {}) {
	        return new SiteLogin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.site = source["site"];
	        this.user = source["user"];
	    }
	}
	export class SpeedLimit {
	    enabled: boolean;
	    kbps: number;
	
	    static createFrom(source: any = {}) {
	        return new SpeedLimit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.kbps = source["kbps"];
	    }
	}
	export class TorrentFilesView {
	    files: torrent.FileEntry[];
	    chosen: number[];
	    fileDone: number[];
	
	    static createFrom(source: any = {}) {
	        return new TorrentFilesView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = this.convertValues(source["files"], torrent.FileEntry);
	        this.chosen = source["chosen"];
	        this.fileDone = source["fileDone"];
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
	export class TorrentPreview {
	    hash: string;
	    name: string;
	    files: torrent.FileEntry[];
	    total: number;
	    directory: string;
	    existing: string[];
	    inList: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TorrentPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.name = source["name"];
	        this.files = this.convertValues(source["files"], torrent.FileEntry);
	        this.total = source["total"];
	        this.directory = source["directory"];
	        this.existing = source["existing"];
	        this.inList = source["inList"];
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
	export class TorrentRow {
	    hash: string;
	    name: string;
	    dir: string;
	    folder: string;
	    // Go type: time
	    added: any;
	    finished: boolean;
	    uploaded: number;
	    stats: torrent.Stats;
	
	    static createFrom(source: any = {}) {
	        return new TorrentRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.name = source["name"];
	        this.dir = source["dir"];
	        this.folder = source["folder"];
	        this.added = this.convertValues(source["added"], null);
	        this.finished = source["finished"];
	        this.uploaded = source["uploaded"];
	        this.stats = this.convertValues(source["stats"], torrent.Stats);
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
	export class TorrentSettings {
	    blocklist: string;
	    keepSharing: boolean;
	    uploadLimitOn: boolean;
	    uploadLimitKB: number;
	    stopRatioOn: boolean;
	    stopRatio: number;
	    stopMinutesOn: boolean;
	    stopMinutes: number;
	
	    static createFrom(source: any = {}) {
	        return new TorrentSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.blocklist = source["blocklist"];
	        this.keepSharing = source["keepSharing"];
	        this.uploadLimitOn = source["uploadLimitOn"];
	        this.uploadLimitKB = source["uploadLimitKB"];
	        this.stopRatioOn = source["stopRatioOn"];
	        this.stopRatio = source["stopRatio"];
	        this.stopMinutesOn = source["stopMinutesOn"];
	        this.stopMinutes = source["stopMinutes"];
	    }
	}
	export class VideoLookUp {
	    title: string;
	    duration: number;
	    choices: tools.Choice[];
	    subs: tools.SubChoice[];
	    needsFfmpeg: boolean;
	    list?: tools.ListPage;
	
	    static createFrom(source: any = {}) {
	        return new VideoLookUp(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.duration = source["duration"];
	        this.choices = this.convertValues(source["choices"], tools.Choice);
	        this.subs = this.convertValues(source["subs"], tools.SubChoice);
	        this.needsFfmpeg = source["needsFfmpeg"];
	        this.list = this.convertValues(source["list"], tools.ListPage);
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
	export class VideoRequest {
	    url: string;
	    title: string;
	    choice: tools.Choice;
	    directory: string;
	    subs: tools.Subs;
	
	    static createFrom(source: any = {}) {
	        return new VideoRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.title = source["title"];
	        this.choice = this.convertValues(source["choice"], tools.Choice);
	        this.directory = source["directory"];
	        this.subs = this.convertValues(source["subs"], tools.Subs);
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

}

export namespace network {
	
	export class DNSPreset {
	    ID: string;
	    Name: string;
	    Plain: string[];
	    TLSName: string;
	    DoH: string;
	
	    static createFrom(source: any = {}) {
	        return new DNSPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.Plain = source["Plain"];
	        this.TLSName = source["TLSName"];
	        this.DoH = source["DoH"];
	    }
	}

}

export namespace queue {
	
	export class Date {
	    year: number;
	    month: number;
	    day: number;
	
	    static createFrom(source: any = {}) {
	        return new Date(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.year = source["year"];
	        this.month = source["month"];
	        this.day = source["day"];
	    }
	}
	export class TimeOfDay {
	    hour: number;
	    minute: number;
	    second: number;
	
	    static createFrom(source: any = {}) {
	        return new TimeOfDay(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hour = source["hour"];
	        this.minute = source["minute"];
	        this.second = source["second"];
	    }
	}
	export class Schedule {
	    startOnStartup?: boolean;
	    startAt?: TimeOfDay;
	    days: boolean[];
	    stopAt?: TimeOfDay;
	    once?: Date;
	    retries?: number;
	    openWhenDone?: string;
	    exitWhenDone?: boolean;
	    turnOff?: string;
	    forceClose?: boolean;
	    wake?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Schedule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startOnStartup = source["startOnStartup"];
	        this.startAt = this.convertValues(source["startAt"], TimeOfDay);
	        this.days = source["days"];
	        this.stopAt = this.convertValues(source["stopAt"], TimeOfDay);
	        this.once = this.convertValues(source["once"], Date);
	        this.retries = source["retries"];
	        this.openWhenDone = source["openWhenDone"];
	        this.exitWhenDone = source["exitWhenDone"];
	        this.turnOff = source["turnOff"];
	        this.forceClose = source["forceClose"];
	        this.wake = source["wake"];
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
	export class Queue {
	    id: string;
	    name: string;
	    filesAtOnce: number;
	    items: string[];
	    sync?: boolean;
	    schedule: Schedule;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Queue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.filesAtOnce = source["filesAtOnce"];
	        this.items = source["items"];
	        this.sync = source["sync"];
	        this.schedule = this.convertValues(source["schedule"], Schedule);
	        this.createdAt = this.convertValues(source["createdAt"], null);
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
	

}

export namespace remote {
	
	export class Config {
	    on: boolean;
	    port: number;
	    listen: string;
	    hosts: string[];
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.on = source["on"];
	        this.port = source["port"];
	        this.listen = source["listen"];
	        this.hosts = source["hosts"];
	    }
	}
	export class Device {
	    id: string;
	    name: string;
	    added: number;
	    seen: number;
	
	    static createFrom(source: any = {}) {
	        return new Device(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.added = source["added"];
	        this.seen = source["seen"];
	    }
	}
	export class Status {
	    state: string;
	    addrs?: netip.AddrPort[];
	    port?: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.addrs = this.convertValues(source["addrs"], netip.AddrPort);
	        this.port = source["port"];
	        this.error = source["error"];
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

}

export namespace tools {
	
	export class Choice {
	    label: string;
	    selector: string;
	    ext: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new Choice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.selector = source["selector"];
	        this.ext = source["ext"];
	        this.size = source["size"];
	    }
	}
	export class Event {
	    // Go type: time
	    at: any;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = this.convertValues(source["at"], null);
	        this.text = source["text"];
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
	export class ListPage {
	    title: string;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new ListPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.total = source["total"];
	    }
	}
	export class SubChoice {
	    code: string;
	    name: string;
	    automatic: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SubChoice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.automatic = source["automatic"];
	    }
	}
	export class SubProblem {
	    code: string;
	    name: string;
	    tooManyRequests: boolean;
	    reason: string;
	    beside: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SubProblem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.tooManyRequests = source["tooManyRequests"];
	        this.reason = source["reason"];
	        this.beside = source["beside"];
	    }
	}
	export class Subs {
	    langs: string[];
	    automatic: boolean;
	    embed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Subs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.langs = source["langs"];
	        this.automatic = source["automatic"];
	        this.embed = source["embed"];
	    }
	}
	export class VideoProgress {
	    state: string;
	    file?: string;
	    error?: string;
	    downloaded: number;
	    total: number;
	    speed: number;
	    events: Event[];
	    sideFiles: string[];
	    subProblems: SubProblem[];
	
	    static createFrom(source: any = {}) {
	        return new VideoProgress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.file = source["file"];
	        this.error = source["error"];
	        this.downloaded = source["downloaded"];
	        this.total = source["total"];
	        this.speed = source["speed"];
	        this.events = this.convertValues(source["events"], Event);
	        this.sideFiles = source["sideFiles"];
	        this.subProblems = this.convertValues(source["subProblems"], SubProblem);
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

}

export namespace torrent {
	
	export class FileEntry {
	    path: string;
	    size: number;
	    padding: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.size = source["size"];
	        this.padding = source["padding"];
	    }
	}
	export class Peer {
	    addr: string;
	    client: string;
	    fetched: number;
	    uploaded: number;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new Peer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addr = source["addr"];
	        this.client = source["client"];
	        this.fetched = source["fetched"];
	        this.uploaded = source["uploaded"];
	        this.kind = source["kind"];
	    }
	}
	export class Stats {
	    phase: string;
	    done: number;
	    total: number;
	    uploaded: number;
	    downSpeed: number;
	    upSpeed: number;
	    fileDone: number[];
	    peersLive: number;
	    peersSeen: number;
	
	    static createFrom(source: any = {}) {
	        return new Stats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.done = source["done"];
	        this.total = source["total"];
	        this.uploaded = source["uploaded"];
	        this.downSpeed = source["downSpeed"];
	        this.upSpeed = source["upSpeed"];
	        this.fileDone = source["fileDone"];
	        this.peersLive = source["peersLive"];
	        this.peersSeen = source["peersSeen"];
	    }
	}

}


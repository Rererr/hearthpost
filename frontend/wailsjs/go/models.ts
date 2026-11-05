export namespace main {
	
	export class State {
	    name: string;
	    fingerprint: string;
	    saveDir: string;
	    port: number;
	    addresses: string[];
	    startError: string;
	    peers: transfer.Peer[];
	    os: string;
	    prompts: transfer.Prompt[];
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.fingerprint = source["fingerprint"];
	        this.saveDir = source["saveDir"];
	        this.port = source["port"];
	        this.addresses = source["addresses"];
	        this.startError = source["startError"];
	        this.peers = this.convertValues(source["peers"], transfer.Peer);
	        this.os = source["os"];
	        this.prompts = this.convertValues(source["prompts"], transfer.Prompt);
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

export namespace transfer {
	
	export class Peer {
	    fingerprint: string;
	    name: string;
	    addr: string;
	    paired: boolean;
	    online: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Peer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fingerprint = source["fingerprint"];
	        this.name = source["name"];
	        this.addr = source["addr"];
	        this.paired = source["paired"];
	        this.online = source["online"];
	    }
	}
	export class Prompt {
	    id: string;
	    kind: string;
	    peerName: string;
	    peerFingerprint: string;
	    code: string;
	    fileCount: number;
	    totalSize: number;
	    topNames: string[];
	    text: string;
	    expiresAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Prompt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.peerName = source["peerName"];
	        this.peerFingerprint = source["peerFingerprint"];
	        this.code = source["code"];
	        this.fileCount = source["fileCount"];
	        this.totalSize = source["totalSize"];
	        this.topNames = source["topNames"];
	        this.text = source["text"];
	        this.expiresAt = source["expiresAt"];
	    }
	}

}


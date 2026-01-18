export namespace main {
	
	export class SiteMeta {
	    name: string;
	    path: string;
	    icon: string;
	    domain: string;
	    entryPath: string;
	
	    static createFrom(source: any = {}) {
	        return new SiteMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.icon = source["icon"];
	        this.domain = source["domain"];
	        this.entryPath = source["entryPath"];
	    }
	}

}


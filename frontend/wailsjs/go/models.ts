export namespace ai {
	
	export class CLIInfo {
	    name: string;
	    type: string;
	    path: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new CLIInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.path = source["path"];
	        this.version = source["version"];
	    }
	}
	export class ToolCall {
	    id: string;
	    // Go type: struct { Name string "json:\"name\""; Arguments string "json:\"arguments\"" }
	    function: any;
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.function = this.convertValues(source["function"], Object);
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
	export class Message {
	    role: string;
	    content: string;
	    tool_calls?: ToolCall[];
	    tool_call_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.tool_calls = this.convertValues(source["tool_calls"], ToolCall);
	        this.tool_call_id = source["tool_call_id"];
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

export namespace asset_entity {
	
	export class Asset {
	    ID: number;
	    Name: string;
	    Type: string;
	    GroupID: number;
	    Tags: string;
	    Description: string;
	    Config: string;
	    SortOrder: number;
	    Status: number;
	    Createtime: number;
	    Updatetime: number;
	
	    static createFrom(source: any = {}) {
	        return new Asset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.Type = source["Type"];
	        this.GroupID = source["GroupID"];
	        this.Tags = source["Tags"];
	        this.Description = source["Description"];
	        this.Config = source["Config"];
	        this.SortOrder = source["SortOrder"];
	        this.Status = source["Status"];
	        this.Createtime = source["Createtime"];
	        this.Updatetime = source["Updatetime"];
	    }
	}

}

export namespace group_entity {
	
	export class Group {
	    ID: number;
	    Name: string;
	    ParentID: number;
	    SortOrder: number;
	    Createtime: number;
	    Updatetime: number;
	
	    static createFrom(source: any = {}) {
	        return new Group(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.ParentID = source["ParentID"];
	        this.SortOrder = source["SortOrder"];
	        this.Createtime = source["Createtime"];
	        this.Updatetime = source["Updatetime"];
	    }
	}

}

export namespace main {
	
	export class SSHConnectRequest {
	    assetId: number;
	    password: string;
	    key: string;
	    cols: number;
	    rows: number;
	
	    static createFrom(source: any = {}) {
	        return new SSHConnectRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.assetId = source["assetId"];
	        this.password = source["password"];
	        this.key = source["key"];
	        this.cols = source["cols"];
	        this.rows = source["rows"];
	    }
	}

}


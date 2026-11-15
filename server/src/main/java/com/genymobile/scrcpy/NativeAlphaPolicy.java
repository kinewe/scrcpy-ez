package com.genymobile.scrcpy;

import android.content.Context;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.os.Build;
import android.util.DisplayMetrics;
import org.json.JSONArray;
import org.json.JSONObject;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.*;

/** A bounded declarative planner. Stored recipes never contain live selectors,
 * original colours, UIDs, shell commands or arbitrary method names. */
final class NativeAlphaPolicy {
    static final class Rejected extends Exception {
        final Plan plan;
        Rejected(String reason, Plan p) { super(reason); plan=p; }
    }
    static final class Plan {
        NativeAlphaProbe.Node root, camera;
        final LinkedHashMap<String,NativeAlphaProbe.Node> backgrounds=new LinkedHashMap<>();
        JSONArray steps=new JSONArray();
        String restoreEvidence="",signature,kind,strategy,restore="exact",cache="discovered",captureSource="display-mirror";
        boolean sceneEligible,windowEligible;
        JSONObject systemBridge;
        boolean unknownRoot;
        long attemptAt=System.nanoTime();
        double cameraAlphaCoverage;
        long firstReadyAt;
        JSONObject application,environment,stream;
    }
    private final Context context;
    private final String pkg;
    private JSONArray candidates;
    private JSONObject application,environment;
    private String name,serial;
    private JSONObject bridgeSupport;

    NativeAlphaPolicy(Context ctx,String packageName,String encoded)throws Exception {
        context=ctx;pkg=packageName;candidates=new JSONArray();
        if(encoded!=null&&encoded.length()<=65536)try {
            String text=new String(android.util.Base64.decode(encoded,android.util.Base64.URL_SAFE|android.util.Base64.NO_WRAP),StandardCharsets.UTF_8);
            if(text.startsWith("{")){JSONObject input=new JSONObject(text);JSONArray list=input.optJSONArray("recipes");if(list!=null)candidates=list;
                long offset=System.currentTimeMillis()-input.optLong("hostNow",System.currentTimeMillis());for(int i=0;i<candidates.length();i++){JSONObject r=candidates.optJSONObject(i);if(r!=null&&r.optLong("retryAfter")>0)r.put("retryAfter",r.optLong("retryAfter")+offset);}
            }
            else if(!"null".equals(text))candidates=new JSONArray(text);
        }catch(Exception ignored){}
        if(candidates.length()>16)candidates=new JSONArray();
        serial=property("ro.serialno");if(serial.isEmpty())try{serial=Build.getSerial();}catch(Exception ignored){}
        refreshApplication();
    }
    private static String property(String key){try{return (String)Class.forName("android.os.SystemProperties").getMethod("get",String.class).invoke(null,key);}catch(Exception e){return "";}}
    static String digest(String text)throws Exception {return hex(MessageDigest.getInstance("SHA-256").digest(text.getBytes(StandardCharsets.UTF_8)));}
    private static String hex(byte[] bytes){StringBuilder s=new StringBuilder();for(byte b:bytes)s.append(String.format(Locale.ROOT,"%02x",b&255));return s.toString();}
    synchronized void refreshApplication()throws Exception {
        PackageManager pm=context.getPackageManager();PackageInfo info=pm.getPackageInfo(pkg,PackageManager.GET_SIGNING_CERTIFICATES);
        application=new JSONObject().put("versionName",info.versionName==null?"":info.versionName).put("versionCode",info.getLongVersionCode()).put("lastUpdateTime",info.lastUpdateTime);
        try{Signature[] signatures=info.signingInfo.getApkContentsSigners();List<String> hashes=new ArrayList<>();for(Signature s:signatures)hashes.add(hex(MessageDigest.getInstance("SHA-256").digest(s.toByteArray())));Collections.sort(hashes);application.put("signingSha256",String.join(",",hashes));}catch(Exception ignored){}
        name=pm.getApplicationLabel(info.applicationInfo).toString();
        bridgeSupport=null;try{bridgeSupport=NativeAlphaScene.support(context);}catch(Exception ignored){}
        environment=new JSONObject().put("serial",serial).put("sdk",Build.VERSION.SDK_INT).put("manufacturer",Build.MANUFACTURER).put("model",Build.MODEL).put("fingerprint",Build.FINGERPRINT).put("userId",info.applicationInfo.uid/100000);
    }
    synchronized void viewport(DisplayMetrics m)throws Exception {environment.put("width",m.widthPixels).put("height",m.heightPixels).put("density",m.densityDpi).put("uiMode",context.getResources().getConfiguration().uiMode);}
    private static String className(NativeAlphaProbe.Node n){return n.id.substring(0,n.id.indexOf('@'));}
    private static String stableId(NativeAlphaProbe.Node n){String s=n.property("mID");if(s==null)s=n.property("getId()");return s==null?"":s;}
    private static String signature(List<NativeAlphaProbe.Node> nodes,NativeAlphaProbe.Node root,NativeAlphaProbe.Node camera)throws Exception {
        // Fingerprint the rendering path, not animated foreground controls.
        // A scan light changing size must not retrigger camera mutations.
        Set<NativeAlphaProbe.Node> relevant=new HashSet<>();
        for(NativeAlphaProbe.Node branch=camera;branch!=null;branch=branch.parent){relevant.add(branch);if(branch==root)break;
            int w=Math.min(branch.number("layout:getWidth()"),root.number("layout:getWidth()")),h=Math.min(branch.number("layout:getHeight()"),root.number("layout:getHeight()"));
            for(NativeAlphaProbe.Node n:nodes){if(n==branch)break;if(n.parent==branch.parent&&n.number("layout:getWidth()")>=w&&n.number("layout:getHeight()")>=h&&(n.background()!=null||!"null".equals(n.property("bg_"))))relevant.add(n);}
        }
        StringBuilder s=new StringBuilder();int rw=Math.max(1,root.number("layout:getWidth()")),rh=Math.max(1,root.number("layout:getHeight()"));
        for(NativeAlphaProbe.Node n:nodes)if(relevant.contains(n)){s.append(n.depth).append(':').append(className(n)).append(':').append(stableId(n)).append(':').append(n.number("layout:getWidth()")*16L/rw).append('x').append(n.number("layout:getHeight()")*16L/rh).append(';');}
        return digest(s.toString());
    }
    private static void step(Plan p,String target,String op,int value)throws Exception {p.steps.put(new JSONObject().put("target",target).put("operation",op).put("value",value));}
    static boolean templateMatchesScope(JSONObject r,String pkg,String activity,String kind){return r!=null&&r.optInt("schema")==1&&pkg.equals(r.optString("package"))&&activity.equals(r.optString("activity"))&&kind.equals(r.optString("cameraKind"))&&"supported".equals(r.optString("status"));}
    static boolean independentUi(List<NativeAlphaProbe.Node> nodes,NativeAlphaProbe.Node camera){
        Set<NativeAlphaProbe.Node> path=new HashSet<>();for(NativeAlphaProbe.Node n=camera;n!=null;n=n.parent)path.add(n);
        for(NativeAlphaProbe.Node n:nodes)if(!path.contains(n)&&n.number("layout:getWidth()")>8&&n.number("layout:getHeight()")>8&&("VISIBLE".equals(n.property("getVisibility()"))||"0".equals(n.property("getVisibility()"))))return true;return false;
    }
    static boolean previewLeaf(List<NativeAlphaProbe.Node> nodes,NativeAlphaProbe.Node candidate){
        // TextureView and SurfaceView are Views, not ViewGroups. A named container is not a preview.
        for(NativeAlphaProbe.Node n:nodes)if(n.parent==candidate)return false;return true;
    }
    static boolean knownSurfaceLayer(NativeAlphaProbe.Node n){String v=n.property("mSubLayer");return "-2".equals(v)||"1".equals(v);}
    static boolean sameBuild(JSONObject a,JSONObject b){return a.optLong("versionCode",-1)==b.optLong("versionCode",-2)&&a.optString("versionName").equals(b.optString("versionName"))&&a.optString("signingSha256").equals(b.optString("signingSha256"));}
    static boolean sameObservationEnvironment(JSONObject a,JSONObject b){for(String k:new String[]{"sdk","fingerprint","userId","width","height","density","uiMode"})if(!String.valueOf(a.opt(k)).equals(String.valueOf(b.opt(k))))return false;return true;}
    static boolean allowedSteps(JSONArray steps,Plan p){
        if(steps.length()==0||steps.length()>24)return false;Set<String> seen=new HashSet<>();boolean alpha=false,onTop=false;
        for(int i=0;i<steps.length();i++){JSONObject s=steps.optJSONObject(i);if(s==null)return false;String role=s.optString("target"),op=s.optString("operation");double v=s.optDouble("value",-1);
            if(!seen.add(role+":"+op))return false;
            if("clearBackground".equals(op)){if(v!=0||!p.backgrounds.containsKey(role))return false;}
            else if("setCameraAlpha".equals(op)){if(!"camera".equals(role)||v!=0)return false;alpha=true;}
            else if("surfaceOnTop".equals(op)){if(!"camera".equals(role)||!"surface".equals(p.kind)||v!=1)return false;onTop=true;}
            else if("prepareSystemScene".equals(op)){if(!p.sceneEligible||!"system-scene".equals(p.strategy)||!"root".equals(role)||v!=1)return false;}
            else if("prepareWindowSource".equals(op)){if(!p.windowEligible||!"window-direct".equals(p.strategy)||!"root".equals(role)||v!=1)return false;}
            else return false;
        }
        for(String role:p.backgrounds.keySet())if(!seen.contains(role+":clearBackground"))return false;
        return alpha&&(!"surface".equals(p.kind)||onTop);
    }
    synchronized Plan plan(String activity,List<NativeAlphaProbe.Node> nodes,String previousCamera,Map<String,Integer> originalColours,boolean unknownRoot)throws Exception {
        Plan p=new Plan();p.application=new JSONObject(application.toString());p.environment=new JSONObject(environment.toString());List<NativeAlphaProbe.Node> previews=new ArrayList<>();
        for(NativeAlphaProbe.Node n:nodes){if(n.id.startsWith("com.android.internal.policy.DecorView@"))p.root=n;String cls=className(n);
            if((n.previewKind!=null||cls.endsWith("TextureView")||cls.endsWith("SurfaceView"))&&n.number("layout:getWidth()")>0&&n.number("layout:getHeight()")>0){String visible=n.property("getVisibility()");if("VISIBLE".equals(visible)||"0".equals(visible))previews.add(n);}}
        if(p.root==null||previews.isEmpty())return null;
        previews.removeIf(n -> !previewLeaf(nodes,n));
        final long rootArea=(long)p.root.number("layout:getWidth()")*p.root.number("layout:getHeight()");
        previews.removeIf(n->(long)n.number("layout:getWidth()")*n.number("layout:getHeight()")<rootArea/6||n.number("layout:getWidth()")<p.root.number("layout:getWidth()")/3);
        if(previews.isEmpty())return null;
        previews.sort((a,b)->Long.compare((long)b.number("layout:getWidth()")*b.number("layout:getHeight()"),(long)a.number("layout:getWidth()")*a.number("layout:getHeight()")));
        p.camera=previews.get(0);p.kind=p.camera.previewKind!=null?p.camera.previewKind:className(p.camera).endsWith("TextureView")?"texture":"surface";p.strategy="texture".equals(p.kind)?"texture-background":"surface-zalpha";p.signature=signature(nodes,p.root,p.camera);
        p.sceneEligible=p.root.sceneFeature!=0&&NativeAlphaScene.eligiblePreview(p.kind,bridgeSupport!=null,p.root.number("layout:getWidth()"),p.root.number("layout:getHeight()"),environment.optInt("width"),environment.optInt("height"))&&p.root.number("layout:getLocationOnScreen_x()")==0&&p.root.number("layout:getLocationOnScreen_y()")==0;
        p.windowEligible=NativeAlphaScene.windowSourceReady()&&NativeAlphaScene.viewportCompatible(p.root.number("layout:getWidth()"),p.root.number("layout:getHeight()"),environment.optInt("width"),environment.optInt("height"))&&p.root.number("layout:getLocationOnScreen_x()")==0&&p.root.number("layout:getLocationOnScreen_y()")==0;
        if(previews.size()>1&&(long)previews.get(1).number("layout:getWidth()")*previews.get(1).number("layout:getHeight()")>(long)p.camera.number("layout:getWidth()")*p.camera.number("layout:getHeight()")/2)throw new Rejected("ambiguous_preview",p);
        if(!independentUi(nodes,p.camera))throw new Rejected("shared_ui_surface",p);
        JSONObject cached=null,guide=null,sceneGuide=null;String source="discovered";boolean guided=false;
        for(int i=0;i<candidates.length();i++){JSONObject r=candidates.optJSONObject(i);if(!templateMatchesScope(r,pkg,activity,p.kind))continue;
            JSONObject selector=r.optJSONObject("cameraSelector");
            if(sceneGuide==null&&p.sceneEligible&&"supported".equals(r.optString("status"))&&("system-scene".equals(r.optString("strategy"))||"wechat-system-scene".equals(r.optString("strategy")))&&p.kind.equals(r.optString("cameraKind"))&&"window-buffer-layer".equals(r.optString("captureSource"))&&NativeAlphaScene.sameRoute(bridgeSupport,r.optJSONObject("systemBridge")))sceneGuide=r;
            // Device/build fields describe an observation. They never admit or exclude a rule.
            if(guide==null&&"supported".equals(r.optString("status"))&&p.kind.equals(r.optString("cameraKind"))&&selector!=null&&!stableId(p.camera).isEmpty()&&!"NO_ID".equals(stableId(p.camera))&&className(p.camera).equals(selector.optString("class"))&&stableId(p.camera).equals(selector.optString("resourceId")))guide=r;
            if(!p.signature.equals(r.optString("treeSignature"))||!p.kind.equals(r.optString("cameraKind"))||!"supported".equals(r.optString("status")))continue;
            if(cached==null){cached=r;source="capability";}

        }
        if(cached==null&&sceneGuide!=null){cached=sceneGuide;source="capability";}
        if(cached==null&&guide!=null){cached=guide;source="capability";guided=true;}
        boolean allowSurface=NativeAlphaScene.surfaceReady();
        if("surface".equals(p.kind)&&!allowSurface)throw new Rejected("surface_api_unavailable",p);
        p.unknownRoot=unknownRoot||p.root.background()==null&&!p.root.backgroundKnownNull&&p.root.backgroundResource==null;
        if(p.root.backgroundResource!=null&&p.root.resourceFromTheme){p.restore="themeResource";p.restoreEvidence="theme-resource-class-match";}
        if(p.unknownRoot&&!("surface".equals(p.kind)&&allowSurface)&&!p.sceneEligible)throw new Rejected("unrestorable_root_background",p);
        if("surface".equals(p.kind)&&p.camera.surfaceOrder==null&&!knownSurfaceLayer(p.camera))p.restore="closeScanner";
        if(p.root.backgroundResource!=null)p.backgrounds.put("root",p.root);
        if(p.unknownRoot){p.restore="closeScanner";p.backgrounds.put("root",p.root);}
        else if(p.root.background()!=null&&((p.root.background()>>>24)!=0||originalColours.containsKey(p.root.id)))p.backgrounds.put("root",p.root);
        {
            int depth=0;for(NativeAlphaProbe.Node n=p.camera.parent;n!=null&&n!=p.root;n=n.parent){Integer bg=n.background();if(bg!=null&&((bg>>>24)!=0||originalColours.containsKey(n.id)))p.backgrounds.put("ancestor/"+depth,n);else if(bg==null&&!n.backgroundKnownNull)throw new Rejected("unrestorable_ancestor_background",p);++depth;}
            int index=0;Set<String> selected=new HashSet<>();
            for(NativeAlphaProbe.Node branch=p.camera;branch!=null&&branch!=p.root;branch=branch.parent){
                // A center-cropped SurfaceView may be several times wider than
                // its window. Backdrops only have to cover its visible region.
                int requiredW=Math.min(branch.number("layout:getWidth()"),p.root.number("layout:getWidth()"));
                int requiredH=Math.min(branch.number("layout:getHeight()"),p.root.number("layout:getHeight()"));
                for(NativeAlphaProbe.Node n:nodes){if(n==branch)break;
                    if(n.parent==branch.parent&&n.number("layout:getWidth()")>=requiredW&&n.number("layout:getHeight()")>=requiredH){
                        Integer bg=n.background();if(bg!=null&&((bg>>>24)!=0||originalColours.containsKey(n.id))&&selected.add(n.id))p.backgrounds.put("backing/"+(index++),n);
                    }
                }
            }
        }
        if(p.backgrounds.size()>20)throw new Rejected("background_plan_too_large",p);
        for(String role:p.backgrounds.keySet())step(p,role,"clearBackground",0);
        if("surface".equals(p.kind))step(p,"camera","surfaceOnTop",1);
        step(p,"camera","setCameraAlpha",0);
        if(cached!=null&&"window-direct".equals(cached.optString("strategy"))&&p.windowEligible&&!"closeScanner".equals(p.restore))promoteWindow(p);
        if(cached!=null&&("system-scene".equals(cached.optString("strategy"))||"wechat-system-scene".equals(cached.optString("strategy")))&&p.sceneEligible&&NativeAlphaScene.sameRoute(bridgeSupport,cached.optJSONObject("systemBridge")))promoteScene(p);
        if(cached!=null&&p.kind.equals(cached.optString("cameraKind"))&&p.restore.equals(cached.optString("restore"))&&allowedSteps(cached.optJSONArray("steps")==null?new JSONArray():cached.optJSONArray("steps"),p)){p.steps=cached.getJSONArray("steps");p.cache=source;}
        else if(guided)p.cache="capability";
        return p;
    }
    synchronized void promoteScene(Plan p)throws Exception {
        if(!p.sceneEligible||bridgeSupport==null)throw new java.io.IOException("system_scene_unvalidated");
        p.strategy="system-scene";p.captureSource="window-buffer-layer";p.systemBridge=new JSONObject(bridgeSupport.toString());
        for(int i=p.steps.length()-1;i>=0;i--)if("prepareWindowSource".equals(p.steps.getJSONObject(i).optString("operation")))p.steps.remove(i);
        boolean present=false;for(int i=0;i<p.steps.length();i++)if("prepareSystemScene".equals(p.steps.getJSONObject(i).optString("operation")))present=true;
        if(!present)step(p,"root","prepareSystemScene",1);
    }
    synchronized void promoteWindow(Plan p)throws Exception {
        if(!p.windowEligible||"closeScanner".equals(p.restore))throw new java.io.IOException("direct_window_unrestorable");
        p.strategy="window-direct";p.captureSource="window-buffer-layer";p.systemBridge=null;
        for(int i=p.steps.length()-1;i>=0;i--)if("prepareSystemScene".equals(p.steps.getJSONObject(i).optString("operation")))p.steps.remove(i);
        boolean present=false;for(int i=0;i<p.steps.length();i++)if("prepareWindowSource".equals(p.steps.getJSONObject(i).optString("operation")))present=true;
        if(!present)step(p,"root","prepareWindowSource",1);
    }
    synchronized void mirrorSource(Plan p)throws Exception {
        p.strategy="texture".equals(p.kind)?"texture-background":"surface-zalpha";p.captureSource="display-mirror";p.systemBridge=null;
        for(int i=p.steps.length()-1;i>=0;i--)if("prepareWindowSource".equals(p.steps.getJSONObject(i).optString("operation")))p.steps.remove(i);
    }
    static String ruleKey(Plan p)throws Exception {
        return digest("alpha-rule-v1|"+p.signature+"|"+p.kind+"|"+p.strategy+"|"+p.captureSource+"|"+p.restore+"|"+p.steps.toString());
    }
    static String sceneFailureReason(Throwable error){
        for(int i=0;i<4&&error.getCause()!=null;i++)error=error.getCause();
        String message=error.getMessage();return "system_scene_"+(message!=null&&message.matches("[a-z][a-z0-9_]{1,72}")?message:error.getClass().getSimpleName());
    }
    synchronized void result(String activity,Plan p,String reason,int frames,boolean spatial,boolean updates,long seenAt)throws Exception {
        boolean success=reason==null;long now=System.currentTimeMillis(),readyAt=success&&p.firstReadyAt>0?p.firstReadyAt:System.nanoTime();
        JSONObject record=new JSONObject().put("schema",1).put("engineVersion",18).put("ruleKey",ruleKey(p)).put("package",pkg).put("name",name).put("application",p.application).put("environment",p.environment).put("activity",activity).put("treeSignature",p.signature).put("cameraKind",p.kind).put("cameraSelector",new JSONObject().put("class",className(p.camera)).put("resourceId",stableId(p.camera))).put("strategy",p.strategy).put("stream",p.stream).put("captureSource",p.captureSource).put("systemBridge",p.systemBridge).put("steps",p.steps).put("restore",p.restore).put("restoreEvidence",p.restoreEvidence).put("status",success?"supported":"unsupported").put("cache",p.cache).put("observedAt",now)
            .put("verification",new JSONObject().put("alphaFrames",frames).put("spatialAlpha",spatial).put("updatesObserved",updates).put("cameraFunctionalVerified",false).put("cameraAlphaCoverage",p.cameraAlphaCoverage).put("decisionMs",Math.max(0,(readyAt-seenAt)/1000000L)).put("probeMs",Math.max(0,(readyAt-p.attemptAt)/1000000L)));
        if(!success){record.put("reason",reason).put("retryAfter",now+(reason.startsWith("system_scene_")?15000L:6*60*60*1000L));}
        // Negative cached results are not rewritten on every revisit.
        System.out.println("NATIVE_ALPHA_RESULT "+record);
        JSONArray next=new JSONArray().put(record);
        for(int i=0;i<candidates.length()&&next.length()<16;i++){JSONObject old=candidates.optJSONObject(i);if(old==null)continue;
            boolean same=activity.equals(old.optString("activity"))&&p.signature.equals(old.optString("treeSignature"))&&sameBuild(p.application,old.optJSONObject("application")==null?new JSONObject():old.optJSONObject("application"));
            JSONObject env=old.optJSONObject("environment");same&=env!=null&&serial.equals(env.optString("serial"))&&sameObservationEnvironment(p.environment,env);
            if(!same)next.put(old);
        }candidates=next;
    }
    static boolean spatialAlpha(int minimum,int maximum,int transparent,int nonzero,int count){return minimum<maximum&&transparent>count/100&&nonzero>count/1000;}
    private static double scalar(NativeAlphaProbe.Node n,String name,double fallback){try{String value=n.property("drawing:"+name+"()");if(value==null)return fallback;double v=Double.parseDouble(value);return Double.isFinite(v)?v:fallback;}catch(Exception e){return fallback;}}
    static double[] screenBounds(NativeAlphaProbe.Node n){
        double a=1,b=0,c=0,d=1;
        for(NativeAlphaProbe.Node v=n;v!=null;v=v.parent){
            if(Math.abs(scalar(v,"getRotationX",0))>0.01||Math.abs(scalar(v,"getRotationY",0))>0.01)return null;
            double rad=Math.toRadians(scalar(v,"getRotation",0)),co=Math.cos(rad),si=Math.sin(rad),sx=scalar(v,"getScaleX",1),sy=scalar(v,"getScaleY",1);
            double na=co*sx*a-si*sy*b,nb=si*sx*a+co*sy*b,nc=co*sx*c-si*sy*d,nd=si*sx*c+co*sy*d;a=na;b=nb;c=nc;d=nd;
        }
        double w=n.number("layout:getWidth()"),h=n.number("layout:getHeight()"),x=n.number("layout:getLocationOnScreen_x()"),y=n.number("layout:getLocationOnScreen_y()");
        double[] xs={0,a*w,c*h,a*w+c*h},ys={0,b*w,d*h,b*w+d*h};double minX=0,maxX=0,minY=0,maxY=0;for(int i=0;i<4;i++){minX=Math.min(minX,xs[i]);maxX=Math.max(maxX,xs[i]);minY=Math.min(minY,ys[i]);maxY=Math.max(maxY,ys[i]);}return new double[]{x+minX,y+minY,x+maxX,y+maxY};
    }
    static int[] cameraSampleRect(Plan p,int w,int h){
        double[] r=screenBounds(p.camera);if(r==null)return new int[4];
        for(NativeAlphaProbe.Node n=p.camera.parent;n!=null;n=n.parent){double[] clip=screenBounds(n);if(clip==null)return new int[4];r[0]=Math.max(r[0],clip[0]);r[1]=Math.max(r[1],clip[1]);r[2]=Math.min(r[2],clip[2]);r[3]=Math.min(r[3],clip[3]);}
        if(r[2]<=r[0]||r[3]<=r[1])return new int[4];double dx=(r[2]-r[0])/4,dy=(r[3]-r[1])/4;r[0]+=dx;r[2]-=dx;r[1]+=dy;r[3]-=dy;
        int ox="window-buffer-layer".equals(p.captureSource)?p.root.number("layout:getLocationOnScreen_x()"):0,oy="window-buffer-layer".equals(p.captureSource)?p.root.number("layout:getLocationOnScreen_y()"):0;
        return new int[]{Math.max(0,(int)Math.ceil(r[0]-ox)),Math.max(0,(int)Math.ceil(r[1]-oy)),Math.min(w,(int)Math.floor(r[2]-ox)),Math.min(h,(int)Math.floor(r[3]-oy))};
    }
    static double cameraAlphaCoverage(Plan p,int[] pixels,int w,int h){
        int[] r=cameraSampleRect(p,w,h);int total=0,partial=0;for(int y=r[1];y<r[3];y++)for(int x=r[0];x<r[2];x++){total++;if((pixels[y*w+x]>>>24)<255)partial++;}return total==0?0:partial/(double)total;
    }
    static double cameraAlphaCoverage(Plan p,byte[] rgba,int w,int h,boolean sampled){
        return cameraAlphaCoverage(p,w,h,sampled,pixel->rgba[pixel*4+3]&255);
    }
    static double cameraAlphaCoverage(Plan p,int w,int h,boolean sampled,java.util.function.IntUnaryOperator alpha){
        int[] r=cameraSampleRect(p,w,h);int sx=sampled?Math.max(1,(r[2]-r[0])/128):1,sy=sampled?Math.max(1,(r[3]-r[1])/128):1,total=0,partial=0;
        for(int y=r[1];y<r[3];y+=sy)for(int x=r[0];x<r[2];x+=sx){total++;if(alpha.applyAsInt(y*w+x)<255)partial++;}return total==0?0:partial/(double)total;
    }
    static void selfTest()throws Exception {
        if(spatialAlpha(128,128,100,100,100))throw new AssertionError("uniform alpha");
        if(spatialAlpha(0,0,100,0,100))throw new AssertionError("blank alpha");
        if(!spatialAlpha(76,255,99,100,100))throw new AssertionError("no zero alpha is valid");
        if(sameBuild(new JSONObject("{\"versionCode\":1,\"versionName\":\"1\"}"),new JSONObject("{\"versionCode\":2,\"versionName\":\"1\"}")))throw new AssertionError("version code mismatch");
        if(sameObservationEnvironment(new JSONObject("{\"sdk\":36}"),new JSONObject("{\"sdk\":35}")))throw new AssertionError("device capability mismatch");
        Plan p=new Plan();p.kind="texture";
        if(allowedSteps(new JSONArray("[{\"target\":\"camera\",\"operation\":\"exec\",\"value\":0}]"),p))throw new AssertionError("arbitrary operation");
        JSONArray sceneSteps=new JSONArray("[{\"target\":\"camera\",\"operation\":\"setCameraAlpha\",\"value\":0},{\"target\":\"root\",\"operation\":\"prepareSystemScene\",\"value\":1}]");
        if(allowedSteps(sceneSteps,p))throw new AssertionError("unvalidated system scene");
        p.sceneEligible=true;p.strategy="system-scene";if(!allowedSteps(sceneSteps,p))throw new AssertionError("validated scene rejected");
        if(NativeAlphaScene.sameSupport(new JSONObject("{\"versionCode\":36}"),new JSONObject("{\"versionCode\":37}")))throw new AssertionError("changed system bridge reused");
        if(!NativeAlphaScene.eligiblePreview("texture",true,1440,3200,1440,3200)||!NativeAlphaScene.eligiblePreview("texture",true,2136,3200,2136,3200)||!NativeAlphaScene.eligiblePreview("surface",true,1440,3200,1440,3200))throw new AssertionError("capability-compatible viewport rejected");
        if(NativeAlphaScene.eligiblePreview("texture",false,1440,3200,1440,3200)||NativeAlphaScene.eligiblePreview("unknown",true,1440,3200,1440,3200)||NativeAlphaScene.eligiblePreview("texture",true,576,1280,1440,3200))throw new AssertionError("missing capability accepted");
        if(NativeAlphaScene.transitionTheme("")!=-1||NativeAlphaScene.transitionTheme("android:attr/windowActivityTransitions\nfalse\n")!=0||NativeAlphaScene.transitionTheme("android:attr/windowActivityTransitions\ntrue\n")!=1)throw new AssertionError("unknown theme incorrectly treated as missing feature");
        if(android.os.Build.VERSION.SDK_INT==36&&!NativeAlphaScene.frameworkReady())throw new AssertionError("framework preflight");
        NativeAlphaProbe.Node root=new NativeAlphaProbe.Node(),cam=new NativeAlphaProbe.Node();root.line=cam.line="layout:getWidth()=2,32 layout:getHeight()=2,32 layout:getLocationOnScreen_x()=1,0 layout:getLocationOnScreen_y()=1,0";p.root=root;p.camera=cam;
        int[] px=new int[1024];Arrays.fill(px,0xff000000);Arrays.fill(px,0,32,0);
        if(cameraAlphaCoverage(p,px,32,32)!=0)throw new AssertionError("transparent status bar is not a camera hole");
        Arrays.fill(px,0x4c000000);if(cameraAlphaCoverage(p,px,32,32)!=1)throw new AssertionError("native camera coverage");
        byte[] rgba=new byte[px.length*4];for(int i=0;i<px.length;i++)rgba[i*4+3]=(byte)(px[i]>>>24);
        if(cameraAlphaCoverage(p,rgba,32,32,false)!=1||cameraAlphaCoverage(p,rgba,32,32,true)!=1)throw new AssertionError("RGBA camera coverage");
        Arrays.fill(rgba,(byte)255);if(cameraAlphaCoverage(p,rgba,32,32,false)!=0||cameraAlphaCoverage(p,rgba,32,32,true)!=0)throw new AssertionError("opaque RGBA camera accepted");
        NativeAlphaProbe.Node light=new NativeAlphaProbe.Node();root.id="android.view.View@1";cam.id="android.view.TextureView@2";light.id="android.view.View@3";cam.parent=light.parent=root;cam.depth=light.depth=1;light.line="layout:getWidth()=1,2 layout:getHeight()=1,2";
        List<NativeAlphaProbe.Node> tree=Arrays.asList(root,cam,light);String fingerprint=signature(tree,root,cam);
        light.line="layout:getWidth()=2,31 layout:getHeight()=2,31";
        if(!fingerprint.equals(signature(tree,root,cam)))throw new AssertionError("foreground animation invalidated rendering fingerprint");
        JSONObject template=new JSONObject("{\"schema\":1,\"package\":\"org.example\",\"activity\":\"org.example.Scan\",\"cameraKind\":\"surface\",\"status\":\"supported\"}");
        if(!templateMatchesScope(template,"org.example","org.example.Scan","surface"))throw new AssertionError("portable template requires diagnostics");
        if(templateMatchesScope(template,"org.other","org.example.Scan","surface")||templateMatchesScope(template,"org.example","org.example.Scan","texture"))throw new AssertionError("template scope not bound");
        p.signature=fingerprint;String portable=ruleKey(p);p.application=new JSONObject("{\"versionCode\":999,\"lastUpdateTime\":100}");p.environment=new JSONObject("{\"serial\":\"OTHER\",\"fingerprint\":\"different/rom\",\"sdk\":35,\"userId\":10}");
        if(!portable.equals(ruleKey(p)))throw new AssertionError("diagnostics leaked into runtime rule identity");
        p.restore="closeScanner";if(portable.equals(ruleKey(p)))throw new AssertionError("restore boundary absent from runtime rule");
        NativeAlphaProbe.Node surface=new NativeAlphaProbe.Node();surface.line="drawing:mSubLayer=2,-2";if(!knownSurfaceLayer(surface))throw new AssertionError("known surface layer");surface.line="drawing:mSubLayer=1,9";if(knownSurfaceLayer(surface))throw new AssertionError("unknown surface layer accepted");
        cam.line=cam.line.replace("layout:getWidth()=2,32","layout:getWidth()=2,16");
        if(fingerprint.equals(signature(tree,root,cam)))throw new AssertionError("camera geometry did not invalidate fingerprint");
        String inventory="  Layer [41] org.example.long/org.example.long.Scanner#41\n  Layer [42] org.example.long/org.example.long.Scanner#42\n  Layer [43] SurfaceView[org.example.long/org.example.long.Scanner]#43\n  truncated[...].Scanner#41 flags:8448 a:1 pid=77 uid=10370\n  truncated[...].Scanner#42 flags:8448 a:1 pid=88 uid=1000\n  SurfaceView[...]#43 parent=41 flags:1282 a:0 pid=77 uid=10370\n";
        if(!NativeAlphaScene.scopedLayerIds(inventory,"org.example.long/org.example.long.Scanner",10370).equals(java.util.Collections.singleton(41)))throw new AssertionError("scoped truncated layer inventory");
        System.out.println("PASS NativeAlphaPolicy: uniform/blank/statusbar rejection, camera coverage, metadata references, capability preflight, operation allowlist, stable animated foreground fingerprint");
    }
    public static void main(String[] args)throws Exception {if(args.length==1&&"--self-test".equals(args[0]))selfTest();else throw new IllegalArgumentException();}
}

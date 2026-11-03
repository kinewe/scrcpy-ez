package com.genymobile.scrcpy;
import java.lang.reflect.*;
import org.json.*;
/** Read-only framework signatures; no target View, package or window mutation. */
public final class OpaqueApiInventory {
    public static void main(String[] args)throws Exception{
        if(android.os.Process.myUid()!=2000)throw new SecurityException();JSONArray classes=new JSONArray();
        for(String name:new String[]{"android.view.View","com.android.internal.policy.DecorView","android.view.ViewDebug","android.view.ViewRootImpl","android.view.ThreadedRenderer"}){
            Class<?> type=Class.forName(name);JSONArray methods=new JSONArray();
            for(Method method:type.getDeclaredMethods()){
                if(!method.getName().toLowerCase(java.util.Locale.ROOT).matches(".*(opaque|opacity|format|transparen|layertype|layoutparam|viewroot|hardwarerenderer).*"))continue;
                JSONArray parameters=new JSONArray();boolean basic=true;
                for(Class<?> parameter:method.getParameterTypes()){parameters.put(parameter.getName());basic&=parameter.isPrimitive()||parameter==String.class;}
                methods.put(new JSONObject().put("name",method.getName()).put("parameters",parameters).put("returns",method.getReturnType().getName()).put("public",Modifier.isPublic(method.getModifiers())).put("static",Modifier.isStatic(method.getModifiers())).put("basicParameters",basic));
            }
            classes.put(new JSONObject().put("class",name).put("isViewReceiver",android.view.View.class.isAssignableFrom(type)).put("declaredMethods",methods));
        }
        System.out.println("OPAQUE_API_INVENTORY_JSON "+new JSONObject().put("sdk",android.os.Build.VERSION.SDK_INT).put("classes",classes).put("targetMutations",0));System.exit(0);
    }
}

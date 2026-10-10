package org.scrcpyez.notificationlab;
import android.app.*;
import android.content.*;
import android.os.*;
import android.widget.TextView;
import java.io.*;

final class Fixture {
    static final String CHANNEL="scez_lab";
    static void post(Context context, Intent command) {
        String tag=command.getStringExtra("tag");int id=command.getIntExtra("case",0);
        if(tag==null||!tag.matches("scez_app_detail_[a-f0-9]+")||id<1||id>2)return;
        NotificationManager manager=(NotificationManager)context.getSystemService(Context.NOTIFICATION_SERVICE);
        manager.createNotificationChannel(new NotificationChannel(CHANNEL,"ez synthetic details",NotificationManager.IMPORTANCE_DEFAULT));
        Intent detail=new Intent(context,DetailActivity.class).putExtra("tag",tag).putExtra("case",id).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        ActivityOptions options=ActivityOptions.makeBasic();
        if(Build.VERSION.SDK_INT>=35)options.setPendingIntentCreatorBackgroundActivityStartMode(ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_DENIED);
        PendingIntent action=PendingIntent.getActivity(context,id,detail,PendingIntent.FLAG_IMMUTABLE|PendingIntent.FLAG_UPDATE_CURRENT,options.toBundle());
        record(context,"post-result.json","{\"enabled\":"+manager.areNotificationsEnabled()+",\"case\":"+id+"}");
        manager.notify(tag,id,new Notification.Builder(context,CHANNEL).setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle("ez 合成应用详情 "+id).setContentText("原始不可变通知动作 / 非导出详情页").setContentIntent(action).build());
    }
    static void record(Context context,String name,String value){
        try(FileOutputStream out=context.openFileOutput(name,Context.MODE_PRIVATE)){out.write(value.getBytes(java.nio.charset.StandardCharsets.UTF_8));}catch(Exception ignored){}
    }
    static void show(Activity activity,String text){TextView view=new TextView(activity);view.setText(text);view.setTextSize(24);view.setPadding(30,50,30,30);activity.setContentView(view);}
    static void detail(Activity activity,Intent command){
        int id=command.getIntExtra("case",0);String tag=command.getStringExtra("tag");
        show(activity,"ez notification detail "+id);
        try(FileOutputStream out=activity.openFileOutput("detail-result.json",Context.MODE_PRIVATE)){
            String value="{\"case\":"+id+",\"display\":"+activity.getWindowManager().getDefaultDisplay().getDisplayId()+",\"pid\":"+android.os.Process.myPid()+",\"tag\":\""+tag+"\"}";
            out.write(value.getBytes(java.nio.charset.StandardCharsets.UTF_8));
        }catch(Exception ignored){}
    }
}

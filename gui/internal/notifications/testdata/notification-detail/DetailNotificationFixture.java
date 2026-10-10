package lab;

import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.Workarounds;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.provider.Settings;
import java.io.BufferedReader;
import java.io.InputStreamReader;

// Own synthetic messages only. Keeps the original immutable actions alive
// until the host closes stdin, then cancels its own notifications and channel.
public final class DetailNotificationFixture {
    public static void main(String[] args) throws Exception {
        android.os.Looper.prepareMainLooper();
        Workarounds.apply();
        Context context=FakeContext.get();
        String tag=args[0];
        NotificationManager manager=(NotificationManager)context.getSystemService(Context.NOTIFICATION_SERVICE);
        manager.createNotificationChannel(new NotificationChannel(tag,"ez detail laboratory",NotificationManager.IMPORTANCE_DEFAULT));
        BufferedReader input=new BufferedReader(new InputStreamReader(System.in,java.nio.charset.StandardCharsets.UTF_8));
        PendingIntent[] actions=new PendingIntent[4];
        try {
            System.out.println("FIXTURE_READY");System.out.flush();
            String line;
            while((line=input.readLine())!=null) {
                int id=Integer.parseInt(line);
                if(id<1||id>3)break;
                String pkg=id==2?"com.android.systemui":"com.android.settings";
                Intent detail=args.length>1?new Intent(id==2?Settings.ACTION_SOUND_SETTINGS:Settings.ACTION_DISPLAY_SETTINGS)
                    :new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,Uri.parse("package:"+pkg));
                detail.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                PendingIntent action=PendingIntent.getActivity(context,id,detail,PendingIntent.FLAG_IMMUTABLE|PendingIntent.FLAG_UPDATE_CURRENT);
                actions[id]=action;
                Notification card=new Notification.Builder(context,tag).setSmallIcon(android.R.drawable.ic_dialog_info)
                        .setContentTitle("ez synthetic detail "+id).setContentText(id==3?"验证码：123456":"Open the original application details page")
                        .setContentIntent(action).build();
                manager.notify(tag,id,card);
                System.out.println("POSTED_"+id);System.out.flush();
            }
        } finally {
            for(int id=1;id<=3;id++){manager.cancel(tag,id);if(actions[id]!=null)actions[id].cancel();}
            manager.deleteNotificationChannel(tag);
        }
    }
}

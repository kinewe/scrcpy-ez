package org.scrcpyez.notificationlab;
import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;
public class DetailActivity extends Activity {
    @Override public void onCreate(Bundle state){super.onCreate(state);Fixture.detail(this,getIntent());}
    @Override public void onNewIntent(Intent command){super.onNewIntent(command);setIntent(command);Fixture.detail(this,command);}
}

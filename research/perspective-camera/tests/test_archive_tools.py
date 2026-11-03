from pathlib import Path
import importlib.util
import json
import os
import runpy
import subprocess
import sys
import unittest
from unittest.mock import patch

REPO = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPO / 'research'))
import research_common as config

spec = importlib.util.spec_from_file_location('graphics_inventory', REPO / 'research/alipay-texture-20261102/run-graphics-capabilities.py')
inventory_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inventory_module)


class ConfigurationTests(unittest.TestCase):
    def test_no_implicit_device(self):
        with patch.dict(os.environ, {}, clear=True), patch.object(sys, 'argv', ['probe']):
            with self.assertRaises(SystemExit): config.serial()

    def test_explicit_option_and_port(self):
        with patch.dict(os.environ, {'RC25_RESEARCH_ADB_PORT': '5053'}, clear=True), patch.object(sys, 'argv', ['probe', '--serial', 'device-A']):
            self.assertEqual(config.serial(), 'device-A')
            self.assertEqual(config.adb_port(), '5053')

    def test_rejects_unsafe_serials(self):
        for serial in ['', 'device; command', 'device\nother', '-s device']:
            with self.assertRaises(SystemExit): config.validate_serial(serial)

    def test_devices_are_unique_and_explicit(self):
        with patch.dict(os.environ, {'RC25_RESEARCH_DEVICES': json.dumps({'phone': 'device-A', 'tablet': 'device-B'})}, clear=True):
            self.assertEqual(config.serials(), ['device-A', 'device-B'])
        with patch.dict(os.environ, {'RC25_RESEARCH_DEVICES': json.dumps({'phone': 'device-A', 'tablet': 'device-A'})}, clear=True):
            with self.assertRaises(SystemExit): config.devices()

    def test_invalid_port(self):
        for port in ['0', '65536', 'text']:
            with patch.dict(os.environ, {'RC25_RESEARCH_ADB_PORT': port}, clear=True):
                with self.assertRaises(SystemExit): config.adb_port()


class InventoryTests(unittest.TestCase):
    def runner(self, offline_check=False, bad_capture=False):
        calls = []
        def run(command, **kwargs):
            calls.append(command)
            if command[-1].endswith('else echo ABSENT; fi'):
                return subprocess.CompletedProcess(command, 1 if offline_check else 0, '' if offline_check else 'ABSENT\n', 'offline' if offline_check else '')
            if 'app_process' in command:
                return subprocess.CompletedProcess(command, 1 if bad_capture else 0,
                    '' if bad_capture else 'GRAPHICS_CAPABILITIES_JSON {"apps":[],"sdk":36}\n', 'capture failed' if bad_capture else '')
            return subprocess.CompletedProcess(command, 0, '', '')
        return run, calls

    def test_two_devices_have_separate_owned_paths_and_cleanup(self):
        runner, calls = self.runner()
        result = inventory_module.inventory('adb-test', '5053', ['device-A', 'device-B'], Path('probe.jar'), runner)
        self.assertTrue(all(entry['ok'] and entry['cleanup']['temporaryJarAbsent'] for entry in result))
        pushes = [command[-1] for command in calls if command[5] == 'push']
        self.assertEqual(len(set(pushes)), 2)
        for command in calls: self.assertEqual(command[1:3], ['-P', '5053'])
        self.assertFalse(any('force-stop' in command or 'settings' in command for command in calls))

    def test_disconnect_is_not_successful_cleanup(self):
        runner, _ = self.runner(offline_check=True)
        entry = inventory_module.inventory('adb-test', '5037', ['device-A'], Path('probe.jar'), runner)[0]
        self.assertFalse(entry['ok'])
        self.assertFalse(entry['cleanup']['temporaryJarAbsent'])

    def test_capture_failure_still_cleans_own_file(self):
        runner, calls = self.runner(bad_capture=True)
        entry = inventory_module.inventory('adb-test', '5037', ['device-A'], Path('probe.jar'), runner)[0]
        self.assertFalse(entry['ok'])
        self.assertTrue(entry['cleanup']['temporaryJarAbsent'])
        self.assertTrue(any(command[5:8] == ['shell', 'rm', '-f'] for command in calls))

    def test_historical_deployer_stops_before_process_launch(self):
        with patch('subprocess.run') as run, patch('subprocess.check_output') as output:
            with self.assertRaisesRegex(SystemExit, 'HISTORICAL SESSION SCRIPT'):
                runpy.run_path(str(REPO / 'research/shutdown-alipay-20261101/deploy.py'), run_name='__main__')
            run.assert_not_called()
            output.assert_not_called()


if __name__ == '__main__': unittest.main()

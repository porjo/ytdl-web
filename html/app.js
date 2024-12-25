
var player = null;

var loc = window.location, ws_uri;
if (loc.protocol === "https:") {
	ws_uri = "wss:";
} else {
	ws_uri = "ws:";
}
ws_uri += "//" + loc.host;
var path = loc.pathname.replace(/\/$/, '');
ws_uri += path + "/websocket";

var progLast = null;
var seekTimer = null;

var trackId = null;

$(function(){
	var ws = new WebSocket(ws_uri);

	const url = new URL(window.location);
	const searchParams = new URLSearchParams(url.search);
	var inputURL = searchParams.get('url');
	if (inputURL) {
		$("#url").val(inputURL);
	}

	$("#url").on('paste', function(e) {
		let data = e.originalEvent.clipboardData.getData('Text');
		searchParams.set('url', encodeURI(data));
		url.search = searchParams;
		window.history.pushState('newUrl', '', url);
	});

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			$("#spinner").show();
			$("#progress-bar > span").css("width", "0%")
				.text("0%");
			let url = $("#url").val();
			let val = {URL: url};
			ws.send(JSON.stringify(val));
			$("#status").prepend("Requesting URL " + url + "\n");
			//$(this).prop('disabled', true);
		} else {
			$("#status").prepend("socket not ready\n")
		}
	});

	$("#searchbox span").click(function() {
		$("#url").val('');
	});

	$("#recent_header #controls").click(function() {
		let urls = [];
		$(".recent_url.selected").each(function() {
			urls.push($(this).find(".stream_play").data("stream_url"));
			$(this).remove();
		});

		if( urls.length > 0 ) {
			let param = {delete_urls: urls};
			ws.send(JSON.stringify(param));
		}
	});

	function updateJob (msg) {
		let $job = $('#job-' + msg.Value.Id);
		if ($job.length == 0) {
			$job = $('<div>', { id: 'job-' + msg.Value.Id, class: 'job' }).appendTo('#output');
			$('<div>', { class: 'progress-bar', html: '<span>0%</span>' }).appendTo($job);
			$('<div>', {
				class: 'details', html:
					'<label>Video Title:</label><span class="title"></span>' +
					'<br>' +
					'<label>ETA:</label><span class="eta"></span>' +
					'<br>' +
					'<label>Size:</label><span class="filesize"></span>' +
					'<div class="status"></div>'
			}).appendTo($job);
		}

		if (!('Title' in msg.Value)) {
			return $job;
		}

		let title = msg.Value.Title;

		let fileSize = '';
		if ('FileSize' in msg.Value) {
			let bytes = msg.Value.FileSize;
			if( bytes == 0 && 'Progress' in msg.Value) {
				bytes = msg.Value.Progress.FileSize;
			}
			fileSize = (bytes / 1024 / 1024).toFixed(2) + " MB";
		}

		let eta = '';
		let pct = 0;
		if ('Progress' in msg.Value) {
			pct = msg.Value.Progress.Pct > 100 ? 100 : msg.Value.Progress.Pct;
			eta = msg.Value.Progress.ETA;
		}

		$job.find('.title').text(title);
		$job.find('.progress-bar > span').css("width", pct + "%")
			.text(pct.toFixed(1) + "%");
		$job.find('.eta').text(eta);
		$job.find('.filesize').text(fileSize);

		return $job
	}

	/*
	ws.onopen = function() {
	};
	*/

	ws.onmessage = function (e)	{
		let msg = JSON.parse(e.data);
		if( 'Key' in msg ) {
			switch (msg.Key) {
				case 'error':
					$("#output").show();
					$("#spinner").hide();
					$("#status").prepend("Error: " + msg.Value + "\n");
					break;
				case 'unknown':
					$("#output").show();
					$("#spinner").hide();
					var $job = updateJob(msg);
					$job.find('.status').prepend(msg.Value.Msg);
					break;
				case 'completed':
					$("#spinner").hide();
					var $job = updateJob(msg);
					$job.remove();
					break;
				case 'info':
					$("#output").show();
					$("#spinner").hide();
					var $job = updateJob(msg);
					break;
				case 'link_stream':
					if(!isPlaying()) {
						$("#playa").show();
						updatePlayer(msg.Value.DownloadURL, msg.Value.Title, msg.Value.Artist);
					}
					break;
				case 'recent':
					if( msg.Value.length == 0 ) {
						break;
					}
					$("#recent").show();
					$("#recent_urls").empty();
					for (let i=0; i< msg.Value.length; i++) {
						let artist = msg.Value[i].Artist;
						let title = msg.Value[i].Title;
						// let description = msg.Value[i].Description;
						// if(description.length == 0) description = "n/a";

						let $ru = $("<div>", {class: 'recent_url'});
						let $cont = $("<div>", {class: 'media_meta'});
						$cont.append($("<span>", {class: 'media_artist', text: artist}));
						$cont.append($("<span>", { class: 'media_title', text: title }));
						// let $description = $("<div>", { class: 'media_description', text: description });
						// $cont.append($description);
						$cont.click(function() {
							$(this).closest(".recent_url").toggleClass("selected");
							// $description.slideToggle().addClass('overflow_scroll');
						});
						$ru.append($cont);

						let $media = $("<div>", {class: 'media'});
						$playButton = $("<svg>", {version: "2.0"}).append( $("<use>", {href: "#play-btn"}) );
						let $mediaPlay = $("<div>", {class: 'stream_play', html: $playButton});
						// 'refresh' play button content to allow SVG to display
						$mediaPlay.html($mediaPlay.html());
						$mediaPlay.data("stream_url", msg.Value[i].URL);
						$mediaPlay.data("artist", artist);
						$mediaPlay.data("title", title);
						$mediaPlay.click(streamPlayClick);
						$media.append($mediaPlay);
						const progress = getMediaProgress(title, artist);
						if (progress.duration > 0) {
							const currentTime = new Date(progress.currentTime * 1000).toISOString().slice(11, 19);
							const duration = new Date(progress.duration * 1000).toISOString().slice(11, 19);
							let mediaProgressTxt = currentTime + " / " + duration + " - " + progress.percent.toFixed(0).padStart(3) + "%";
							mediaProgressTxt = mediaProgressTxt.replaceAll(" ", "&nbsp;");
							let $mediaProgress = $("<div>", { class: 'media_progress', html: mediaProgressTxt });
							$media.append($mediaProgress);
						}
						$ru.append($media);
						$("#recent_urls").append($ru);
					}
					break;
			}
		}
	};

	function streamPlayClick() {
		$("#playa").show();
		let url = $(this).data("stream_url");
		let title = $(this).data("title");
		let artist = $(this).data("artist");
		updatePlayer(url, title, artist, true);
	}

	function isPlaying() {
		if(player && !player.audio.paused) {
			return true;
		}
		return false
	}

	function getMediaProgress(title, artist) {
		let trackId = "ytdl-" + title + " - " + artist;
		let obj = JSON.parse(localStorage.getItem(trackId))
		if (obj) {
			return {currentTime: obj.currentTime, duration: obj.duration, percent: (obj.currentTime/obj.duration*100)};
		} else {
			return {currentTime: 0, duration: 0, percent: 0};
		}
	}

	function updatePlayer(url, title, artist, autoplay=false) {

		trackId =  "ytdl-" + title + " - " + artist;
		document.title = trackId;

		if( player === null ) {
			player = new Shikwasa.Player({
				container: () => document.querySelector('#playa'),
				audio: {
					title: title,
					artist: artist,
					src: url
				},
				speedOptions: [1.0, 1.1, 1.2],
				download: true
			});

			player.on('loadedmetadata', (e) => {
				let obj = JSON.parse(localStorage.getItem(trackId))
				if (obj && obj.currentTime > 0) {
					player.seek(obj.currentTime);
					if (obj.playbackRate) {
						player.playbackRate = obj.playbackRate;
					}
				}

				// store latest play time
				seekTimer = setInterval(() => {
					if (isPlaying()) {
						let o = { currentTime: player.currentTime, duration: player.duration, timestamp: new Date().getTime(), playbackRate: player.playbackRate }
						localStorage.setItem(trackId, JSON.stringify(o));
					}
				}, 2000);
			});

		} else {

			if (seekTimer !== null) {
				clearInterval(seekTimer);
			}

			player.update({
				title: title,
				artist: artist,
				src: url
			});
		}

		if(autoplay) {
			player.play();
		}

		// Add space for player at bottom of page
		$("body").css("padding-bottom", $(".shk-player").css("height"));
	}

	ws.onclose = function()	{
		$("#status").prepend("Connection closed\n");
		console.log("Connection closed");
		$("#ws-status-light").toggleClass("on off");
		$("#controls").hide();
	};

	ws.onopen = function(e) {
		$("#status").prepend("Connection opened\n");
		console.log("Connection opened");
		$("#ws-status-light").toggleClass("off on");
		$("#controls").show();
	}

	$('#output').on('click','.status', function() {
		$(this).toggleClass("expand");
	});

	// cleanup storage
	for (let i = 0; i < localStorage.length; i++) {
		let name = localStorage.key(i);
		if( !name.startsWith("ytdl-")) {
			continue
		}
		let o = JSON.parse(localStorage.getItem(name));

		if(o) {
			let delta = new Date().getTime() - o.timestamp;
			// remove if older than 7 days
			if(delta > 7*86400*1000) {
				localStorage.removeItem(name);
			}
		}
	}

});
